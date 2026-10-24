package sessionfile

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"

	"github.com/loadkpi/baton/business/domain/sessionbus"
	"github.com/loadkpi/baton/foundation/jsonl"
)

// ScrubSession masks the payloads of the file of the Session, under the lock
// SaveEvent takes on it.
func (s *Store) ScrubSession(_ context.Context, ref sessionbus.SessionRef, mask func(payload []byte) ([]byte, bool), dryRun bool) (int, error) {
	return jsonl.Rewrite(s.refPath(ref), func(line []byte) ([]byte, bool) {
		e, err := toBusEvent(line)
		if err != nil {
			return nil, false // not an Event: never guessed at, never dropped
		}
		payload, changed := mask(e.Payload)
		if !changed {
			return nil, false
		}
		edited, err := withMaskedPayload(line, payload)
		return edited, err == nil
	}, dryRun)
}

// withMaskedPayload is the line with its payload replaced and "masked" true,
// and every other field as it was, in its order, whether this version of Baton
// knows it or not.
func withMaskedPayload(line, payload []byte) ([]byte, error) {
	dec := json.NewDecoder(bytes.NewReader(line))
	dec.UseNumber()
	if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
		return nil, fmt.Errorf("not an object")
	}
	var out bytes.Buffer
	out.WriteByte('{')
	masked := false
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return nil, err
		}
		key, _ := tok.(string)
		var value json.RawMessage
		if err := dec.Decode(&value); err != nil {
			return nil, err
		}
		switch key {
		case "payload":
			value = payload
		case "masked":
			value, masked = json.RawMessage("true"), true
		}
		if out.Len() > 1 {
			out.WriteByte(',')
		}
		k, _ := json.Marshal(key)
		out.Write(k)
		out.WriteByte(':')
		out.Write(value)
	}
	if !masked {
		out.WriteString(`,"masked":true`)
	}
	out.WriteByte('}')
	return out.Bytes(), nil
}
