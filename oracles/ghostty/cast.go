package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// CastHeader holds the metadata from an asciicast header.
type CastHeader struct {
	Version int
	Width   int
	Height  int
}

// LoadCast reads an asciicast v2 or v3 file from r and returns the terminal
// dimensions plus the concatenated output byte stream. Input ("i"), marker
// ("m"), and resize ("r") events are ignored. CRLF line endings are tolerated.
func LoadCast(r io.Reader) (CastHeader, []byte, error) {
	br := bufio.NewReader(r)
	first, err := br.ReadBytes('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return CastHeader{}, nil, fmt.Errorf("read header: %w", err)
	}
	first = bytes.TrimSpace(first)
	if len(first) == 0 {
		return CastHeader{}, nil, errors.New("empty asciicast")
	}

	var hdr CastHeader
	if err := json.Unmarshal(first, &hdr); err != nil {
		return CastHeader{}, nil, fmt.Errorf("parse header: %w", err)
	}
	if hdr.Version != 2 && hdr.Version != 3 {
		return CastHeader{}, nil, fmt.Errorf("unsupported asciicast version %d", hdr.Version)
	}
	if hdr.Width <= 0 || hdr.Height <= 0 {
		return CastHeader{}, nil, fmt.Errorf("invalid terminal size %dx%d", hdr.Width, hdr.Height)
	}

	var out bytes.Buffer
	dec := json.NewDecoder(br)
	for {
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			// Tolerate trailing garbage or truncated final line.
			break
		}
		data, ok := castEventData(raw, hdr.Version)
		if ok {
			out.WriteString(data)
		}
	}

	return hdr, out.Bytes(), nil
}

// castEventData extracts output data from a single asciicast event. The
// second result is false for non-output events or unparseable lines.
func castEventData(raw json.RawMessage, version int) (string, bool) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return "", false
	}

	if version == 2 {
		// v2 events are [time, "o", "data"] arrays.
		if raw[0] != '[' {
			return "", false
		}
		var ev []json.RawMessage
		if err := json.Unmarshal(raw, &ev); err != nil || len(ev) < 3 {
			return "", false
		}
		var eventType string
		if err := json.Unmarshal(ev[1], &eventType); err != nil {
			return "", false
		}
		if eventType != "o" {
			return "", false
		}
		var data string
		if err := json.Unmarshal(ev[2], &data); err != nil {
			return "", false
		}
		return data, true
	}

	// v3 events are {"time": N, "type": "o", "data": "..."} objects.
	if raw[0] != '{' {
		return "", false
	}
	var ev struct {
		Type string `json:"type"`
		Data string `json:"data"`
	}
	if err := json.Unmarshal(raw, &ev); err != nil {
		return "", false
	}
	if ev.Type != "o" {
		return "", false
	}
	return ev.Data, true
}

// UnmarshalJSON implements a tolerant header parser that accepts both integer
// and floating-point width/height values.
func (h *CastHeader) UnmarshalJSON(data []byte) error {
	var aux struct {
		Version json.Number `json:"version"`
		Width   json.Number `json:"width"`
		Height  json.Number `json:"height"`
	}
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}
	if v, err := aux.Version.Int64(); err == nil {
		h.Version = int(v)
	} else if f, err := aux.Version.Float64(); err == nil {
		h.Version = int(f)
	}
	if v, err := aux.Width.Int64(); err == nil {
		h.Width = int(v)
	} else if f, err := aux.Width.Float64(); err == nil {
		h.Width = int(f)
	}
	if v, err := aux.Height.Int64(); err == nil {
		h.Height = int(v)
	} else if f, err := aux.Height.Float64(); err == nil {
		h.Height = int(f)
	}
	return nil
}
