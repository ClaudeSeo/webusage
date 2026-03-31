package credfinder

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestExpandPath(t *testing.T) {
	home, _ := os.UserHomeDir()

	tests := []struct {
		name    string
		input   string
		want    string
		wantErr bool
	}{
		{
			name:  "~/로 시작하는 경로",
			input: "~/.claude/credentials.json",
			want:  filepath.Join(home, ".claude/credentials.json"),
		},
		{
			name:  "일반 절대경로",
			input: "/tmp/test.json",
			want:  "/tmp/test.json",
		},
		{
			name:  "상대경로 (~ 없음)",
			input: "relative/path",
			want:  "relative/path",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ExpandPath(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("ExpandPath() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("ExpandPath() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestReadJSONCredential(t *testing.T) {
	t.Run("유효한 JSON 파일 읽기", func(t *testing.T) {
		// 임시 JSON 파일 생성
		type testCred struct {
			Token string `json:"token"`
		}
		cred := testCred{Token: "test-token-123"}
		data, _ := json.Marshal(cred)

		tmpFile, err := os.CreateTemp("", "cred-*.json")
		if err != nil {
			t.Fatal(err)
		}
		defer os.Remove(tmpFile.Name())
		tmpFile.Write(data)
		tmpFile.Close()

		var result testCred
		if err := ReadJSONCredential(tmpFile.Name(), &result); err != nil {
			t.Fatalf("ReadJSONCredential() error = %v", err)
		}
		if result.Token != "test-token-123" {
			t.Errorf("Token = %q, want %q", result.Token, "test-token-123")
		}
	})

	t.Run("존재하지 않는 파일", func(t *testing.T) {
		var result map[string]interface{}
		err := ReadJSONCredential("/tmp/nonexistent-cred-file.json", &result)
		if err != ErrNotFound {
			t.Errorf("ReadJSONCredential() error = %v, want ErrNotFound", err)
		}
	})

	t.Run("잘못된 JSON", func(t *testing.T) {
		tmpFile, err := os.CreateTemp("", "bad-*.json")
		if err != nil {
			t.Fatal(err)
		}
		defer os.Remove(tmpFile.Name())
		tmpFile.WriteString("not valid json {{{")
		tmpFile.Close()

		var result map[string]interface{}
		if err := ReadJSONCredential(tmpFile.Name(), &result); err == nil {
			t.Error("ReadJSONCredential() should return error for invalid JSON")
		}
	})
}
