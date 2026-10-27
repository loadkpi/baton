package rawterm

import "unicode/utf8"

// Kind says what a Key is.
type Kind int

// The kinds of key.
const (
	// Rune is a printable character; Key.Rune says which.
	Rune Kind = iota
	Up
	Down
	Enter
	// Esc is the Escape key on its own.
	Esc
	// CtrlC is the byte 0x03, which a terminal in raw mode delivers instead
	// of a signal.
	CtrlC
)

// Key is one key the user pressed.
type Key struct {
	Kind Kind
	Rune rune // for Rune
}

// Decode turns bytes read from a terminal in raw mode into keys. The arrow
// keys come as ESC [ A and ESC [ B (ESC O A and ESC O B in application mode);
// any other escape sequence is dropped; an ESC that is the last byte is the
// Escape key.
func Decode(b []byte) []Key {
	var keys []Key
	for len(b) > 0 {
		switch c := b[0]; {
		case c == 0x1b:
			if len(b) == 1 {
				return append(keys, Key{Kind: Esc})
			}
			if b[1] != '[' && b[1] != 'O' {
				keys = append(keys, Key{Kind: Esc})
				b = b[1:]
				continue
			}
			i := 2
			for i < len(b) && (b[i] < 0x40 || b[i] > 0x7e) {
				i++ // parameters and intermediate bytes
			}
			if i < len(b) {
				switch b[i] {
				case 'A':
					keys = append(keys, Key{Kind: Up})
				case 'B':
					keys = append(keys, Key{Kind: Down})
				}
			}
			b = b[min(i+1, len(b)):]
		case c == '\r' || c == '\n':
			keys = append(keys, Key{Kind: Enter})
			b = b[1:]
		case c == 0x03:
			keys = append(keys, Key{Kind: CtrlC})
			b = b[1:]
		case c < 0x20 || c == 0x7f:
			b = b[1:] // other control bytes
		default:
			r, n := utf8.DecodeRune(b)
			keys = append(keys, Key{Kind: Rune, Rune: r})
			b = b[n:]
		}
	}
	return keys
}
