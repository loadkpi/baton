package sessionbus

import (
	"bytes"
	"context"
	"encoding/json"

	"github.com/loadkpi/baton/foundation/redact"
)

// maskPayload returns the payload with the secrets in its string values masked,
// and whether there were any. The payload is parsed as JSON and every string
// value goes through the Redactor, and a non-empty one under a key that says
// secret is replaced whole (keys and every other value are left alone);
// when nothing changed the payload is returned as it came, otherwise it is
// encoded again, so its keys may be in another order. Whatever cannot be parsed
// is returned as it came: Record has parsed it already.
func (b *Bus) maskPayload(ctx context.Context, payload []byte) (masked []byte, changed bool) {
	return maskPayloadWith(b.redactor(ctx), payload)
}

// maskPayloadWith is maskPayload for a Redactor already made, which Scrub makes
// once for all the lines.
func maskPayloadWith(r *redact.Redactor, payload []byte) (masked []byte, changed bool) {
	dec := json.NewDecoder(bytes.NewReader(payload))
	dec.UseNumber() // a number keeps the digits it was written with
	var doc any
	if err := dec.Decode(&doc); err != nil {
		return payload, false
	}

	doc, changed = maskValue(r, doc)
	if !changed {
		return payload, false
	}
	var out bytes.Buffer
	enc := json.NewEncoder(&out)
	enc.SetEscapeHTML(false) // as the Agent wrote it
	if err := enc.Encode(doc); err != nil {
		return payload, false
	}
	return bytes.TrimRight(out.Bytes(), "\n"), true
}

// redactor masks by pattern and, when the secrets can be read, their values.
// A hook never fails for them: it has no one to tell.
func (b *Bus) redactor(ctx context.Context) *redact.Redactor {
	if b.secrets == nil {
		return redact.New(nil)
	}
	literals, err := b.secrets.QuerySecretValues(ctx)
	if err != nil {
		return redact.New(nil)
	}
	return redact.New(literals)
}

// maskValue masks the strings inside v, in place for objects and arrays. A
// string directly under a key that says secret is replaced whole; under it a
// number, a boolean, null, an empty string, an object or an array is not, and
// the strings inside the last two are looked at one by one.
func maskValue(r *redact.Redactor, v any) (any, bool) {
	changed := false
	switch v := v.(type) {
	case string:
		m := r.Mask(v)
		return m, m != v
	case map[string]any:
		for k, e := range v {
			var c bool
			if s, ok := e.(string); ok && s != "" && redact.SecretKey(k) {
				v[k], c = redact.Mask, s != redact.Mask
			} else {
				v[k], c = maskValue(r, e)
			}
			if c {
				changed = true
			}
		}
	case []any:
		for i, e := range v {
			var c bool
			if v[i], c = maskValue(r, e); c {
				changed = true
			}
		}
	}
	return v, changed
}
