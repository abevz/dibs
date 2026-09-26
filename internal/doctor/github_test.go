package doctor

import (
	"errors"
	"reflect"
	"testing"
)

type githubExec struct {
	outputs [][]byte
	errors  []error
	calls   [][]string
}

func (e *githubExec) Command(name string, args ...string) ([]byte, error) {
	e.calls = append(e.calls, append([]string{name}, args...))
	i := len(e.calls) - 1
	return e.outputs[i], e.errors[i]
}

func (*githubExec) LookupEnv(string) (string, bool) { return "", false }

func TestEvaluateGitHubCLI(t *testing.T) {
	tests := []struct {
		name       string
		outputs    [][]byte
		errors     []error
		status     string
		callCount  int
		wantSecond []string
	}{
		{"missing", [][]byte{nil}, []error{errors.New("missing")}, "WARN", 1, nil},
		{"auth", [][]byte{[]byte("gh version 2.1\n"), nil}, []error{nil, errors.New("logged out")}, "WARN", 2, []string{"gh", "auth", "status", "--hostname", "github.com"}},
		{"api", [][]byte{[]byte("gh version 2.1\n"), nil, []byte("network error")}, []error{nil, nil, errors.New("fail")}, "WARN", 3, nil},
		{"success", [][]byte{[]byte("gh version 2.1\n"), nil, []byte(`{"resources":{"core":{"remaining":123}}}`)}, []error{nil, nil, nil}, "ok", 3, nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			e := &githubExec{outputs: tc.outputs, errors: tc.errors}
			got := EvaluateGitHubCLI(e)
			if got.Name != "GitHub CLI" || got.Status != tc.status || len(e.calls) != tc.callCount {
				t.Fatalf("result = %+v, calls = %v", got, e.calls)
			}
			if tc.wantSecond != nil && !reflect.DeepEqual(e.calls[1], tc.wantSecond) {
				t.Fatalf("auth command = %v", e.calls[1])
			}
			if tc.name == "success" && got.Message != "gh version 2.1; 123 core API requests remaining" {
				t.Fatalf("message = %q", got.Message)
			}
		})
	}
}
