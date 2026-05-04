package protocol

import "encoding/json"

func Marshal(msg *Message) ([]byte, error) {
	return json.Marshal(msg)
}

func Unmarshal(data []byte, msg *Message) error {
	return json.Unmarshal(data, msg)
}
