package signals

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
)

var (
	errInvalid = errors.New("invalid signal")
	sourcePat  = regexp.MustCompile(`^[a-z][a-z0-9_.\-]{0,63}$`)
	kindPat    = sourcePat
)

type inbound struct {
	APIVersion string `json:"apiVersion"`
	Source     string `json:"source"`
	Kind       string `json:"kind"`
	Severity   string `json:"severity"`
	Note       string `json:"note"`
	TTL        *int   `json:"ttl"`
}

// PostFields are the caller-supplied signal fields after decode.
type PostFields struct {
	Source   string
	Kind     string
	Severity string
	Note     string
	TTL      int
}

// DecodePost parses a POST body already bounded to MaxBody.
func DecodePost(body []byte) (PostFields, error) {
	var keys map[string]json.RawMessage
	if err := json.Unmarshal(body, &keys); err != nil {
		return PostFields{}, errInvalid
	}
	if _, ok := keys["id"]; ok {
		return PostFields{}, errInvalid
	}
	if _, ok := keys["created_at"]; ok {
		return PostFields{}, errInvalid
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	var in inbound
	if err := dec.Decode(&in); err != nil {
		return PostFields{}, fmt.Errorf("%w: %v", errInvalid, err)
	}
	return validateInbound(in)
}

func validateInbound(in inbound) (PostFields, error) {
	if in.APIVersion != "vigil.signal/v0" {
		return PostFields{}, errInvalid
	}
	if !sourcePat.MatchString(in.Source) || !kindPat.MatchString(in.Kind) {
		return PostFields{}, errInvalid
	}
	if in.Severity != "info" && in.Severity != "notice" {
		return PostFields{}, errInvalid
	}
	if in.Note != "" {
		if !NoteOK(in.Note) {
			return PostFields{}, errInvalid
		}
	}
	ttl := 0
	if in.TTL != nil {
		ttl = *in.TTL
	}
	return PostFields{
		Source: in.Source, Kind: in.Kind, Severity: in.Severity, Note: in.Note, TTL: ttl,
	}, nil
}
