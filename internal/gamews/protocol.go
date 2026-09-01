package gamews

import (
	"encoding/json"
	"fmt"
	"time"
)

const ProtocolVersion = 1

const (
	TypeCommand = "command"
	TypeResult  = "result"
	TypeEvent   = "event"
	TypePing    = "ping"
	TypePong    = "pong"
)

// Envelope is the control-plane message format shared by game servers and the backend.
type Envelope struct {
	V       int             `json:"v"`
	Type    string          `json:"type"`
	ID      string          `json:"id,omitempty"`
	Name    string          `json:"name,omitempty"`
	OK      *bool           `json:"ok,omitempty"`
	Payload json.RawMessage `json:"payload,omitempty"`
	Error   string          `json:"error,omitempty"`
	TS      int64           `json:"ts,omitempty"`
}

func NewCommand(id, name string, payload any) (Envelope, error) {
	raw, err := marshalPayload(payload)
	if err != nil {
		return Envelope{}, err
	}
	return Envelope{
		V:       ProtocolVersion,
		Type:    TypeCommand,
		ID:      id,
		Name:    name,
		Payload: raw,
		TS:      time.Now().Unix(),
	}, nil
}

func NewResult(id string, ok bool, payload any, errMsg string) (Envelope, error) {
	raw, err := marshalPayload(payload)
	if err != nil {
		return Envelope{}, err
	}
	return Envelope{
		V:       ProtocolVersion,
		Type:    TypeResult,
		ID:      id,
		OK:      &ok,
		Payload: raw,
		Error:   errMsg,
		TS:      time.Now().Unix(),
	}, nil
}

func NewEvent(name string, payload any) (Envelope, error) {
	raw, err := marshalPayload(payload)
	if err != nil {
		return Envelope{}, err
	}
	return Envelope{
		V:       ProtocolVersion,
		Type:    TypeEvent,
		Name:    name,
		Payload: raw,
		TS:      time.Now().Unix(),
	}, nil
}

func NewPing() Envelope {
	return Envelope{V: ProtocolVersion, Type: TypePing, TS: time.Now().Unix()}
}

func NewPong() Envelope {
	return Envelope{V: ProtocolVersion, Type: TypePong, TS: time.Now().Unix()}
}

func marshalPayload(payload any) (json.RawMessage, error) {
	if payload == nil {
		return json.RawMessage("{}"), nil
	}
	switch value := payload.(type) {
	case json.RawMessage:
		if len(value) == 0 {
			return json.RawMessage("{}"), nil
		}
		return value, nil
	case []byte:
		if len(value) == 0 {
			return json.RawMessage("{}"), nil
		}
		if !json.Valid(value) {
			return nil, fmt.Errorf("payload is not valid json")
		}
		return json.RawMessage(value), nil
	default:
		raw, err := json.Marshal(payload)
		if err != nil {
			return nil, err
		}
		return raw, nil
	}
}
