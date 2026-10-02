package events

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strconv"
)

// maxLine bounds a single event; the plugin truncates stdout/stderr well below this.
const maxLine = 16 << 20

// Decoder reads events from a JSONL stream. It never fails on content:
// lines that are not JSON objects come back as Invalid events.
type Decoder struct {
	sc *bufio.Scanner
}

func NewDecoder(r io.Reader) *Decoder {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64<<10), maxLine)
	return &Decoder{sc: sc}
}

// Next returns the next event, or io.EOF at the end of the stream.
// Blank lines are skipped.
func (d *Decoder) Next() (Event, error) {
	for d.sc.Scan() {
		line := bytes.TrimSpace(d.sc.Bytes())
		if len(line) == 0 {
			continue
		}
		return Parse(line), nil
	}
	if err := d.sc.Err(); err != nil {
		return Event{}, err
	}
	return Event{}, io.EOF
}

// Parse decodes one line. Unknown fields and event types are kept as-is
// (callers ignore types they don't handle). A field with an unexpected type
// is left empty rather than dropping the event. Lines that are not JSON
// objects with an "event" field become Invalid.
func Parse(line []byte) Event {
	var e Event
	err := json.Unmarshal(line, &e)
	var typeErr *json.UnmarshalTypeError
	if (err != nil && !errors.As(err, &typeErr)) || e.Type == "" {
		return Event{Type: Invalid, Line: string(line)}
	}
	return e
}

func itoa(n int) string { return strconv.Itoa(n) }
