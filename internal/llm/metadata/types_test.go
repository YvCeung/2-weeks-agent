package metadata

import (
	"encoding/json"
	"fmt"
	"io"
	"reflect"
	"testing"
)

func TestDecodeChatResponse(t *testing.T) {
	type args struct {
		r io.Reader
	}
	tests := []struct {
		name    string
		args    args
		want    *ChatResponse
		wantErr bool
	}{
		// TODO: Add test cases.
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := DecodeChatResponse(tt.args.r)
			if (err != nil) != tt.wantErr {
				t.Errorf("DecodeChatResponse() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("DecodeChatResponse() got = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestOmitempty(t *testing.T) {
	request := ChatRequest{
		Model:       "dk",
		Messages:    make([]ChatMessage, 0),
		Temperature: 0,
		MaxTokens:   1,
		Stream:      true,
	}
	if jsonRequest, err := json.Marshal(request); err == nil {
		fmt.Println(string(jsonRequest))
	}
}
