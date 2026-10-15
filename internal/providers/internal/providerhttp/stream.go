package providerhttp

import (
	"bufio"
	"encoding/json"
	"errors"
	"github.com/yyZe0122/yunmengze-agent/pkg/providerapi"
	"io"
	"strings"
)

// SSE reads bounded event frames, including multi-line data and a final frame
// without an empty line. done distinguishes a protocol completion from EOF.
func SSE(reader io.Reader, maximum int64, handle func(string, []byte) (bool, error)) error {
	scanner := bufio.NewScanner(io.LimitReader(reader, maximum*4+1))
	scanner.Buffer(make([]byte, 4096), int(maximum))
	var event string
	var data strings.Builder
	var total int64
	dispatch := func() (bool, error) {
		if data.Len() == 0 {
			event = ""
			return false, nil
		}
		done, err := handle(event, []byte(strings.TrimSuffix(data.String(), "\n")))
		event = ""
		data.Reset()
		return done, err
	}
	for scanner.Scan() {
		line := scanner.Text()
		total += int64(len(line) + 1)
		if total > maximum*4 {
			return errors.New("provider stream exceeds configured limit")
		}
		if line == "" {
			if done, err := dispatch(); done || err != nil {
				return err
			}
			continue
		}
		field, value, _ := strings.Cut(line, ":")
		value = strings.TrimPrefix(value, " ")
		switch field {
		case "event":
			event = value
		case "data":
			if int64(data.Len()+len(value)) > maximum {
				return errors.New("provider event exceeds configured limit")
			}
			data.WriteString(value)
			data.WriteByte('\n')
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	if done, err := dispatch(); done || err != nil {
		return err
	}
	return io.ErrUnexpectedEOF
}

func StreamError(provider string, raw json.RawMessage) error {
	var detail struct {
		Type    string `json:"type"`
		Code    string `json:"code"`
		Message string `json:"message"`
	}
	_ = json.Unmarshal(raw, &detail)
	message := strings.Join(strings.Fields(detail.Message), " ")
	if message == "" {
		message = "provider stream failed"
	}
	if len(message) > 1500 {
		message = message[:1500]
	}
	kind := providerapi.ErrorProtocol
	retry := false
	switch {
	case detail.Type == "overloaded_error" || detail.Type == "server_error" || detail.Code == "server_error":
		kind, retry = providerapi.ErrorUnavailable, true
	case detail.Type == "rate_limit_error" || detail.Code == "rate_limit_exceeded":
		kind, retry = providerapi.ErrorRateLimited, true
	}
	return &providerapi.ProviderError{Provider: provider, Kind: kind, Retryable: retry, Err: errors.New(message)}
}

func SetStream(body []byte) ([]byte, error) {
	var value map[string]json.RawMessage
	if err := json.Unmarshal(body, &value); err != nil {
		return nil, err
	}
	value["stream"] = json.RawMessage("true")
	return json.Marshal(value)
}
