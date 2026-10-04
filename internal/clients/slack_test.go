package clients

import "testing"

func TestParseCredentials(t *testing.T) {
	for _, input := range []string{`{}`, `null`, `[]`, `{"token":1}`, `{"token":"xoxb-bot"}`, `{"token":"xoxp-"}`, `{"token":" xoxp-user"}`, `{"token":"xoxp-user"} {}`, `{"token":"xoxp-user","extra":"unexpected"}`, `not json`} {
		t.Run(input, func(t *testing.T) {
			if _, err := ParseCredentials([]byte(input)); err == nil {
				t.Fatal("invalid credentials accepted")
			}
		})
	}
	if token, err := ParseCredentials([]byte(`{"token":"xoxp-user"}`)); err != nil || token != "xoxp-user" {
		t.Fatalf("valid credentials: token=%q err=%v", token, err)
	}
}
