package codexkit

import "testing"

func TestTypedHandlerDecodesRequestStruct(t *testing.T) {
	type request struct {
		Name  string `json:"name"`
		Limit int    `json:"limit"`
	}

	handler := TypedHandler(func(req request) (any, error) {
		return req, nil
	})

	result, err := handler(map[string]any{"name": "tool_search", "limit": float64(7)})
	if err != nil {
		t.Fatalf("handler returned error: %v", err)
	}
	got, ok := result.(request)
	if !ok {
		t.Fatalf("result = %T, want request", result)
	}
	if got.Name != "tool_search" || got.Limit != 7 {
		t.Fatalf("decoded request = %+v", got)
	}
}
