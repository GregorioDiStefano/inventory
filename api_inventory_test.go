// Copyright 2016 Mender Software AS
//
//    Licensed under the Apache License, Version 2.0 (the "License");
//    you may not use this file except in compliance with the License.
//    You may obtain a copy of the License at
//
//        http://www.apache.org/licenses/LICENSE-2.0
//
//    Unless required by applicable law or agreed to in writing, software
//    distributed under the License is distributed on an "AS IS" BASIS,
//    WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
//    See the License for the specific language governing permissions and
//    limitations under the License.
package main

import (
	"encoding/json"
	"errors"
	"github.com/ant0ine/go-json-rest/rest"
	"github.com/ant0ine/go-json-rest/rest/test"
	"github.com/mendersoftware/inventory/config"
	"github.com/mendersoftware/inventory/log"
	"github.com/mendersoftware/inventory/requestid"
	"github.com/mendersoftware/inventory/requestlog"
	"github.com/stretchr/testify/assert"
	"net/http"
	"testing"
)

type MockInventory struct {
	mockAddDevice func(d *Device) error
}

func (mi *MockInventory) AddDevice(dev *Device) error {
	return mi.mockAddDevice(dev)
}

func ToJson(data interface{}) string {
	j, _ := json.Marshal(data)
	return string(j)
}

// test.HasHeader only tests the first header,
// so create a wrapper for headers with multiple values
func HasHeader(hdr, val string, r *test.Recorded) bool {
	rec := r.Recorder
	for _, v := range rec.Header()[hdr] {
		if v == val {
			return true
		}
	}

	return false
}

func ExtractHeader(hdr, val string, r *test.Recorded) string {
	rec := r.Recorder
	for _, v := range rec.Header()[hdr] {
		if v == val {
			return v
		}
	}

	return ""
}

func RestError(status string) string {
	msg, _ := json.Marshal(map[string]string{"error": status})
	return string(msg)
}

func makeMockApiHandler(t *testing.T, f InventoryFactory) http.Handler {
	handlers := NewInventoryApiHandlers(f)
	assert.NotNil(t, handlers)

	app, err := handlers.GetApp()
	assert.NotNil(t, app)
	assert.NoError(t, err)

	api := rest.NewApi()
	api.Use(
		&requestlog.RequestLogMiddleware{},
		&requestid.RequestIdMiddleware{},
	)
	api.SetApp(app)

	return api.MakeHandler()
}

func makeJson(t *testing.T, d interface{}) string {
	out, err := json.Marshal(d)
	if err != nil {
		t.FailNow()
	}

	return string(out)
}

func TestApiInventoryAddDevice(t *testing.T) {
	rest.ErrorFieldName = "error"

	testCases := map[string]struct {
		inReq *http.Request

		inventoryErr string

		outCode int
		outBody string
	}{
		"empty body": {
			inReq: test.MakeSimpleRequest("POST",
				"http://1.2.3.4/api/0.1.0/devices",
				nil),
			inventoryErr: "",
			outCode:      400,
			outBody:      RestError("failed to decode request body: JSON payload is empty"),
		},
		"garbled body": {
			inReq: test.MakeSimpleRequest("POST",
				"http://1.2.3.4/api/0.1.0/devices",
				"foo bar"),
			inventoryErr: "",
			outCode:      400,
			outBody:      RestError("failed to decode request body: json: cannot unmarshal string into Go value of type main.Device"),
		},
		"body formatted ok, all fields present": {
			inReq: test.MakeSimpleRequest("POST",
				"http://1.2.3.4/api/0.1.0/devices",
				map[string]interface{}{
					"id": "id-0001",
					"attributes": []map[string]interface{}{
						map[string]interface{}{"name": "a1", "value": "00:00:00:01", "description": "ddd"},
						map[string]interface{}{"name": "a2", "value": 123.2, "description": "ddd"},
						map[string]interface{}{"name": "a3", "value": []interface{}{"00:00:00:01", "00"}, "description": "ddd"},
					},
				},
			),
			inventoryErr: "",
			outCode:      201,
			outBody:      "",
		},
		"body formatted ok, wrong attributes type": {
			inReq: test.MakeSimpleRequest("POST",
				"http://1.2.3.4/api/0.1.0/devices",
				map[string]interface{}{
					"id":         "id-0001",
					"attributes": 123,
				},
			),
			inventoryErr: "",
			outCode:      400,
			outBody:      RestError("failed to decode request body: json: cannot unmarshal number into Go value of type []main.DeviceAttribute"),
		},
		"body formatted ok, 'id' missing": {
			inReq: test.MakeSimpleRequest("POST",
				"http://1.2.3.4/api/0.1.0/devices",
				map[string]interface{}{},
			),
			inventoryErr: "",
			outCode:      400,
			outBody:      RestError("'id' field required"),
		},
		"body formatted ok, incorrect attribute value": {
			inReq: test.MakeSimpleRequest("POST",
				"http://1.2.3.4/api/0.1.0/devices",
				map[string]interface{}{
					"id": "id-0001",
					"attributes": []map[string]interface{}{
						map[string]interface{}{"name": "asd", "value": []interface{}{"asd", 123}},
						map[string]interface{}{"name": "asd2", "value": []interface{}{123, "asd"}},
					},
				},
			),
			inventoryErr: "",
			outCode:      400,
			outBody:      RestError("invalid attribute value provided"),
		},
		"body formatted ok, attribute name missing": {
			inReq: test.MakeSimpleRequest("POST",
				"http://1.2.3.4/api/0.1.0/devices",
				map[string]interface{}{
					"id": "id-0001",
					"attributes": []map[string]interface{}{
						map[string]interface{}{"value": "23"},
					},
				},
			),
			inventoryErr: "",
			outCode:      400,
			outBody:      RestError("attribute 'name' field required"),
		},
		"body formatted ok, inv error": {
			inReq: test.MakeSimpleRequest("POST",
				"http://1.2.3.4/api/0.1.0/devices",
				map[string]interface{}{
					"id": "id-0001",
					"attributes": []map[string]interface{}{
						map[string]interface{}{
							"name":  "name1",
							"value": "value4",
						},
					},
				},
			),
			inventoryErr: "internal error",
			outCode:      500,
			outBody:      RestError("internal error"),
		},
	}

	for name, tc := range testCases {
		t.Logf("test case: %s", name)
		inv := MockInventory{
			mockAddDevice: func(d *Device) error {
				if tc.inventoryErr != "" {
					return errors.New(tc.inventoryErr)
				}
				return nil
			},
		}

		factory := func(c config.Reader, l *log.Logger) (InventoryApp, error) {
			return &inv, nil
		}
		apih := makeMockApiHandler(t, factory)

		recorded := test.RunRequest(t, apih, tc.inReq)
		recorded.CodeIs(tc.outCode)
		recorded.BodyIs(tc.outBody)
	}
}
