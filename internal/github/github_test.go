package github

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestParseIssueRef(t *testing.T) {
	tests := []struct {
		input, key string
	}{
		{"Acme/App#42", "github:acme/app#42"},
		{"https://github.com/Acme/App/issues/42?foo=bar#comment", "github:acme/app#42"},
	}
	for _, tc := range tests {
		t.Run(tc.input, func(t *testing.T) {
			ref, err := ParseIssueRef(tc.input)
			if err != nil || ref.ExternalKey() != tc.key {
				t.Fatalf("ParseIssueRef(%q) = %v, %v", tc.input, ref, err)
			}
		})
	}
	for _, input := range []string{"https://github.com/o/r/pull/1", "https://evil.example/o/r/issues/1", "o/r#0", "o/r#no", "o/r#1/extra", "https://github.com/o/r/issues/1/extra", "https://github.com/o/r/issues/1/", "https://github.com/o/r/issues%2f1", "o/#1", "o/r#99999999999999999999"} {
		t.Run("invalid "+input, func(t *testing.T) {
			if _, err := ParseIssueRef(input); err == nil {
				t.Fatalf("accepted %q", input)
			}
		})
	}
}

func TestCLIWithFakeGH(t *testing.T) {
	dir := t.TempDir()
	script := `#!/bin/sh
case "$*" in
  'api repos/o/r/issues/7') printf '%s' '{"title":"T","body":"B","state":"open","html_url":"https://github.com/o/r/issues/7"}' ;;
  'api repos/o/r/issues/7/comments --paginate --slurp') printf '%s' '[[{"body":"one"}],[{"body":"two"}]]' ;;
  'api repos/o/r/issues/7/comments --method POST --input -') /bin/cat ;;
  *) echo 'unexpected arguments' >&2; exit 1 ;;
esac
`
	if err := os.WriteFile(filepath.Join(dir, "gh"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	ref := IssueRef{"o", "r", 7}
	client := CLI{}
	issue, err := client.GetIssue(context.Background(), ref)
	if err != nil || issue.Title != "T" {
		t.Fatalf("GetIssue = %+v, %v", issue, err)
	}
	comments, err := client.ListComments(context.Background(), ref)
	if err != nil || len(comments) != 2 || comments[1].Body != "two" {
		t.Fatalf("ListComments = %+v, %v", comments, err)
	}
	comment, err := client.CreateComment(context.Background(), ref, "a secret-looking string")
	if err != nil || !strings.Contains(comment.Body, "a secret-looking string") {
		t.Fatalf("CreateComment = %+v, %v", comment, err)
	}
}

func TestClassifyError(t *testing.T) {
	tests := []struct{ message, code string }{
		{"HTTP 404 Not Found", "not_found"},
		{"Bad credentials", "gh_auth"},
		{"API rate limit exceeded", "rate_limited"},
		{"connection refused", "github"},
	}
	for _, tc := range tests {
		err := classifyError(errors.New(tc.message), nil)
		var ghErr *Error
		if !errors.As(err, &ghErr) || ghErr.Code != tc.code {
			t.Fatalf("classify %q = %v", tc.message, err)
		}
	}
}

func TestCLIErrorsWithFakeGH(t *testing.T) {
	for _, tc := range []struct{ name, stderr, code string }{
		{"auth", "gh: Bad credentials", "gh_auth"},
		{"private or missing", "gh: Not Found (HTTP 404)", "not_found"},
		{"rate limit", "gh: API rate limit exceeded", "rate_limited"},
		{"other", "gh: connection refused", "github"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			script := "#!/bin/sh\necho '" + tc.stderr + "' >&2\nexit 1\n"
			if err := os.WriteFile(filepath.Join(dir, "gh"), []byte(script), 0o700); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", dir)
			_, err := (CLI{}).GetIssue(context.Background(), IssueRef{"o", "r", 1})
			var ghErr *Error
			if !errors.As(err, &ghErr) || ghErr.Code != tc.code {
				t.Fatalf("GetIssue error = %v, want %s", err, tc.code)
			}
		})
	}
	t.Run("missing gh", func(t *testing.T) {
		t.Setenv("PATH", t.TempDir())
		_, err := (CLI{}).GetIssue(context.Background(), IssueRef{"o", "r", 1})
		var ghErr *Error
		if !errors.As(err, &ghErr) || ghErr.Code != "gh_missing" {
			t.Fatalf("GetIssue error = %v", err)
		}
	})
	t.Run("timeout", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "gh"), []byte("#!/bin/sh\n/bin/sleep 5\n"), 0o700); err != nil {
			t.Fatal(err)
		}
		t.Setenv("PATH", dir)
		ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
		defer cancel()
		_, err := (CLI{}).GetIssue(ctx, IssueRef{"o", "r", 1})
		var ghErr *Error
		if !errors.As(err, &ghErr) || ghErr.Code != "timeout" {
			t.Fatalf("GetIssue error = %v", err)
		}
	})
}
