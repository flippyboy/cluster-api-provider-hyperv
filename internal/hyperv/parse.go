package hyperv

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

// flexStrings accepts a JSON string or an array. Windows PowerShell 5.1
// ConvertTo-Json unwraps a one-element array unless the script wraps it.
type flexStrings []string

func (f *flexStrings) UnmarshalJSON(b []byte) error {
	b = bytes.TrimSpace(b)
	if len(b) == 0 || bytes.Equal(b, []byte("null")) {
		*f = nil
		return nil
	}
	if b[0] == '"' {
		var s string
		if err := json.Unmarshal(b, &s); err != nil {
			return err
		}
		*f = []string{s}
		return nil
	}
	var many []string
	if err := json.Unmarshal(b, &many); err != nil {
		return err
	}
	*f = many
	return nil
}

func lastJSONObject(out []byte) (json.RawMessage, error) {
	out = bytes.TrimPrefix(out, []byte{0xEF, 0xBB, 0xBF})
	out = bytes.TrimSpace(out)
	var found json.RawMessage
	for i := 0; i < len(out); i++ {
		if out[i] != '{' {
			continue
		}
		dec := json.NewDecoder(bytes.NewReader(out[i:]))
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			continue
		}
		rest := bytes.TrimSpace(out[i+int(dec.InputOffset()):])
		if len(rest) != 0 || len(raw) == 0 || raw[0] != '{' {
			continue
		}
		found = raw
	}
	if len(found) == 0 {
		return nil, fmt.Errorf("no json object in command output")
	}
	return found, nil
}

func jsonErrorField(raw json.RawMessage) error {
	var body struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		return fmt.Errorf("decode command result: %w", err)
	}
	if strings.TrimSpace(body.Error) != "" {
		return fmt.Errorf("%s", strings.TrimSpace(body.Error))
	}
	return nil
}

func clip(b []byte) string {
	s := strings.TrimSpace(string(b))
	const max = 512
	if len(s) > max {
		return s[:max]
	}
	return s
}
