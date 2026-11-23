// Package transcoder converts values to and from the bytes stored in Valkey.
package transcoder

import "encoding/json"

// ITranscoder converts values to and from the bytes stored in Valkey. Implement it to replace the
// default JSON encoding (for example with MessagePack or Protobuf).
type ITranscoder interface {
	Marshal(v any) ([]byte, error)
	Unmarshal(data []byte, v any) error
}

// JSON is the default transcoder, using encoding/json.
//
// Values shared with C# services: System.Text.Json writes PascalCase property names by default.
// encoding/json matches field names case-insensitively when decoding, but writes the Go field name
// (or its json tag), so keep json tags aligned with the C# property names.
type JSON struct{}

var _ ITranscoder = JSON{}

// Marshal encodes v as JSON.
func (JSON) Marshal(v any) ([]byte, error) { return json.Marshal(v) }

// Unmarshal decodes JSON data into v.
func (JSON) Unmarshal(data []byte, v any) error { return json.Unmarshal(data, v) }
