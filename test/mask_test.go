package test

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/loadkpi/baton/internal/batontest"
)

// The tokens below are made up: they have the shape of a real token and are
// built from pieces, so that no scanner takes them for one.
var (
	tokAnthropic = "sk-ant-" + "api03-FAKEfakeFAKE0123456789_-ab"
	tokOpenAI    = "sk-" + "proj" + strings.Repeat("Ab1", 8)
	tokGitHub    = strings.Repeat("Ab1", 12)
	tokFine      = "github_pat_" + "11FAKE0123456789_abcdefghijklmnop"
	tokGitLab    = "glpat-" + "FAKEfake0123456789ab"
	tokSlack     = "xoxb-" + "123456789012-FAKEfakeFAKEfake"
	tokAWS       = "AKIA" + "FAKEFAKE01234567"
	tokGoogle    = "AIza" + strings.Repeat("aB3_", 8) + "xyz"
	tokJWT       = "eyJ" + "hbGciOiJIUzI1NiJ9." + "eyJzdWIiOiJmYWtlIn0." + "FAKEsignature_-0123"
	tokTelegram  = "123456789:" + strings.Repeat("aB3_-", 7)
)

const mask = "[REDACTED]"

// bashPayload is the payload of a finished Bash command of Claude, with extra
// top-level fields.
func bashPayload(t *testing.T, sessionID, command string, extra map[string]any) string {
	t.Helper()
	doc := map[string]any{
		"session_id": sessionID, "cwd": "/x", "hook_event_name": "PostToolUse",
		"tool_name": "Bash", "tool_input": map[string]any{"command": command},
	}
	for k, v := range extra {
		doc[k] = v
	}
	out, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

// recordedCommand runs the hook for a Bash command and returns the command as
// the Event file holds it, and the Event.
func recordedCommand(t *testing.T, env *batontest.Env, command string) (string, map[string]any) {
	t.Helper()
	res := env.RunWithStdin(bashPayload(t, "abc123", command, nil), hookArgs("claude-personal")...)
	if res.ExitCode != 0 || res.Stdout != "" || res.Stderr != "" {
		t.Fatalf("exit code = %d, stdout=%q, stderr=%q, want a silent success", res.ExitCode, res.Stdout, res.Stderr)
	}
	lines := eventLines(t, env, ".baton/sessions/claude-abc123.jsonl")
	event := lines[len(lines)-1]
	payload := event["payload"].(map[string]any)
	return payload["tool_input"].(map[string]any)["command"].(string), event
}

func TestMask_AnEventWithATokenOfAKnownShapeHoldsTheMaskInsteadAndSaysSo(t *testing.T) {
	tests := []struct{ name, command, want string }{
		{"Anthropic key", "echo " + tokAnthropic, "echo " + mask},
		{"OpenAI style key", "echo " + tokOpenAI, "echo " + mask},
		{"GitHub personal", "git clone https://ghp_" + tokGitHub + "@host/x.git", "git clone https://" + mask + "@host/x.git"},
		{"GitHub oauth", "echo gho_" + tokGitHub, "echo " + mask},
		{"GitHub user", "echo ghu_" + tokGitHub, "echo " + mask},
		{"GitHub server", "echo ghs_" + tokGitHub, "echo " + mask},
		{"GitHub refresh", "echo ghr_" + tokGitHub, "echo " + mask},
		{"GitHub fine-grained", "echo " + tokFine, "echo " + mask},
		{"GitLab", "echo " + tokGitLab, "echo " + mask},
		{"Slack bot", "echo " + tokSlack, "echo " + mask},
		{"Slack user", "echo xoxp-" + tokSlack[5:], "echo " + mask},
		{"AWS access key id", "echo " + tokAWS, "echo " + mask},
		{"Google API key", "echo " + tokGoogle, "echo " + mask},
		{"JWT", "echo " + tokJWT, "echo " + mask},
		{"Telegram bot token", "curl https://api.invalid/bot" + tokTelegram + "/getMe", "curl https://api.invalid/bot" + mask + "/getMe"},
		{"Bearer header", "curl -H 'Authorization: Bearer abc.DEF-123_456~xyz' https://x.invalid", "curl -H 'Authorization: Bearer " + mask + "' https://x.invalid"},
		{"bearer in lower case", "curl -H 'authorization: bearer abc.DEF-123_456~xyz'", "curl -H 'authorization: bearer " + mask + "'"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := newEnv(t)

			got, event := recordedCommand(t, env, tt.command)

			if got != tt.want {
				t.Errorf("command = %q, want %q", got, tt.want)
			}
			if event["masked"] != true {
				t.Errorf("masked = %v, want true", event["masked"])
			}
		})
	}
}

func TestMask_APrivateKeyBlockGoesWholeOrToTheEndWhenTheStringIsCut(t *testing.T) {
	body := "MIIBFAKEFAKEFAKE\nabcdefFAKEFAKE0123\n"
	tests := []struct{ name, command, want string }{
		{
			"closed block",
			"echo '-----BEGIN RSA PRIVATE KEY-----\n" + body + "-----END RSA PRIVATE KEY-----' > key.pem",
			"echo '" + mask + "' > key.pem",
		},
		{
			"plain header",
			"-----BEGIN PRIVATE KEY-----\n" + body + "-----END PRIVATE KEY-----\ntail",
			mask + "\ntail",
		},
		{
			"block cut by the end of the string",
			"head\n-----BEGIN OPENSSH PRIVATE KEY-----\n" + body,
			"head\n" + mask,
		},
		{
			"a public key is not a secret",
			"-----BEGIN PUBLIC KEY-----\n" + body + "-----END PUBLIC KEY-----",
			"-----BEGIN PUBLIC KEY-----\n" + body + "-----END PUBLIC KEY-----",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := newEnv(t)

			got, _ := recordedCommand(t, env, tt.command)

			if got != tt.want {
				t.Errorf("command = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestMask_TheValueOfAnAssignmentWithASecretLookingNameGoesAndTheNameStays(t *testing.T) {
	tests := []struct{ name, command, want string }{
		{"NAME=value", "export GITHUB_TOKEN=hunter2hunter2 && make", "export GITHUB_TOKEN=" + mask + " && make"},
		{"NAME: value", "db_password: s3cr3t!", "db_password: " + mask},
		{"JSON member", `{"client_secret": "s3cr3t value", "keep": "this"}`, `{"client_secret": "` + mask + `", "keep": "this"}`},
		{"JSON member without a space", `{"password":"s3cr3t"}`, `{"password":"` + mask + `"}`},
		{"single quotes", "SECRET_KEY='s3cr3t value'", "SECRET_KEY='" + mask + "'"},
		{"any case", "Password=abc", "Password=" + mask},
		{"passwd", "passwd=abc", "passwd=" + mask},
		{"api_key", "API_KEY=abc", "API_KEY=" + mask},
		{"api-key", "x-api-key: abc", "x-api-key: " + mask},
		{"apikey", "apikey=abc", "apikey=" + mask},
		{"private_key", "PRIVATE_KEY=abc", "PRIVATE_KEY=" + mask},
		{"query string", "curl 'https://x.invalid/?token=abc&page=2'", "curl 'https://x.invalid/?token=" + mask + "&page=2'"},
		{"a name that holds no such word", "NAME=value PATH=/bin", "NAME=value PATH=/bin"},
		{"nothing after the sign", "echo token=", "echo token="},
		{"the next line is not the value", "password:\nnext", "password:\nnext"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := newEnv(t)

			got, _ := recordedCommand(t, env, tt.command)

			if got != tt.want {
				t.Errorf("command = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestMask_ValuesOfTheSecretsFileAreMaskedWhenTheyAreAtLeastEightCharactersLong(t *testing.T) {
	env := newEnv(t)
	writeSecrets(t, env, 0o600, "LONG_ONE=plainvalue123\nQUOTED=\"quoted value 9\"\nSHORT=short12\n# COMMENTED=commented99\n")

	got, event := recordedCommand(t, env, "echo plainvalue123 quoted value 9 short12 commented99")

	if want := "echo " + mask + " " + mask + " short12 commented99"; got != want {
		t.Errorf("command = %q, want %q: the two long values masked and the rest as it is", got, want)
	}
	if event["masked"] != true {
		t.Errorf("masked = %v, want true", event["masked"])
	}
}

func TestMask_ALongerValueIsReplacedWholeBeforeAShorterOneInsideIt(t *testing.T) {
	env := newEnv(t)
	writeSecrets(t, env, 0o600, "SHORT_ONE=abcdefgh\nLONG_ONE=abcdefghIJKLMNOP\n")

	got, _ := recordedCommand(t, env, "echo abcdefghIJKLMNOP and abcdefgh")

	if want := "echo " + mask + " and " + mask; got != want {
		t.Errorf("command = %q, want %q", got, want)
	}
}

func TestMask_ASecretInAnyStringValueIsMaskedButNotInKeysNumbersOrTheFieldsTheEventKeeps(t *testing.T) {
	env := newEnv(t)
	writeSecrets(t, env, 0o600, "LITERAL=literalvalue12\n")
	in := `{
  "session_id": "abc123",
  "hook_event_name": "PostToolUse",
  "cwd": "/work/literalvalue12",
  "literalvalue12": "a key is not masked",
  "tool_input": {"command": "echo literalvalue12", "list": ["literalvalue12", 7, true, null, 1.50]},
  "tool_response": {"stdout": "x", "code": 0, "big": 12345678901234567890, "ratio": 0.5, "nothing": null, "ok": false}
}`

	res := env.RunWithStdin(in, hookArgs("claude-personal")...)

	if res.ExitCode != 0 || res.Stdout != "" || res.Stderr != "" {
		t.Fatalf("exit code = %d, stdout=%q, stderr=%q, want a silent success", res.ExitCode, res.Stdout, res.Stderr)
	}
	text := env.ReadFile(".baton/sessions/claude-abc123.jsonl")
	if !strings.Contains(text, `"big":12345678901234567890`) || !strings.Contains(text, `1.50`) {
		t.Errorf("Event line = %q, want the numbers as they were written", text)
	}
	event := eventLines(t, env, ".baton/sessions/claude-abc123.jsonl")[0]
	want := map[string]any{
		"session_id":      "abc123",
		"hook_event_name": "PostToolUse",
		"cwd":             "/work/" + mask,
		"literalvalue12":  "a key is not masked",
		"tool_input": map[string]any{
			"command": "echo " + mask,
			"list":    []any{mask, float64(7), true, nil, 1.5},
		},
		"tool_response": map[string]any{
			"stdout": "x", "code": float64(0), "big": 12345678901234567890.0, "ratio": 0.5, "nothing": nil, "ok": false,
		},
	}
	if !reflect.DeepEqual(event["payload"], want) {
		t.Errorf("payload = %#v, want %#v", event["payload"], want)
	}
	if event["cwd"] != "/work/literalvalue12" || event["session_id"] != "abc123" {
		t.Errorf("cwd = %v, session_id = %v, want the two the Event keeps outside the payload unmasked", event["cwd"], event["session_id"])
	}
	if event["masked"] != true {
		t.Errorf("masked = %v, want true", event["masked"])
	}
}

func TestMask_APayloadWithNothingToMaskIsWrittenAsItCameAndHasNoMaskedField(t *testing.T) {
	env := newEnv(t)
	writeSecrets(t, env, 0o600, "LITERAL=literalsecret1\n")
	// Keys out of order, a number JSON encoders change, an escape and an
	// almost-secret: a re-encoded payload would differ.
	in := `{"session_id": "abc123", "zeta": 1, "hook_event_name": "Stop", "alpha": 1.50, "cwd": "/x", "big": 12345678901234567890, "html": "a < b", "near": "literalsecre", "short": "sk-abc"}`

	res := env.RunWithStdin(in, hookArgs("claude-personal")...)

	if res.ExitCode != 0 {
		t.Fatalf("exit code = %d, stderr=%q", res.ExitCode, res.Stderr)
	}
	text := env.ReadFile(".baton/sessions/claude-abc123.jsonl")
	wantPayload := `"payload":{"session_id":"abc123","zeta":1,"hook_event_name":"Stop","alpha":1.50,"cwd":"/x","big":12345678901234567890,"html":"a < b","near":"literalsecre","short":"sk-abc"}`
	if !strings.Contains(text, wantPayload) {
		t.Errorf("Event line = %q, want it to contain %s", text, wantPayload)
	}
	if strings.Contains(text, `"masked"`) {
		t.Errorf("Event line = %q, want no masked field", text)
	}
}

func TestMask_ASecretsFileOthersCanReadDoesNotStopTheHookAndItsValuesAreNotUsed(t *testing.T) {
	env := newEnv(t)
	writeSecrets(t, env, 0o644, "LITERAL=literalsecret1\n")

	got, event := recordedCommand(t, env, "echo literalsecret1 "+tokGitLab)

	if want := "echo literalsecret1 " + mask; got != want {
		t.Errorf("command = %q, want %q: the patterns still apply, the file's values do not", got, want)
	}
	if event["masked"] != true {
		t.Errorf("masked = %v, want true", event["masked"])
	}
}

func TestMask_AMalformedSecretsFileIsIgnoredSilentlyAndNeverQuoted(t *testing.T) {
	env := newEnv(t)
	writeSecrets(t, env, 0o600, "this line is somebody's secret\nLITERAL=literalsecret1\n")

	res := env.RunWithStdin(bashPayload(t, "abc123", "echo literalsecret1 "+tokAWS, nil), hookArgs("claude-personal")...)

	if res.ExitCode != 0 || res.Stdout != "" || res.Stderr != "" {
		t.Fatalf("exit code = %d, stdout=%q, stderr=%q, want a silent success", res.ExitCode, res.Stdout, res.Stderr)
	}
	text := env.ReadFile(".baton/sessions/claude-abc123.jsonl")
	if !strings.Contains(text, "echo literalsecret1 "+mask) || strings.Contains(text, tokAWS) {
		t.Errorf("Event line = %q, want the pattern masked and the file's values not used", text)
	}
}

func TestMask_AHookInAContainerMasksByPatternWhereSecretsEnvIsNotMounted(t *testing.T) {
	for _, rt := range []string{"sandbox", "compose"} {
		t.Run(rt, func(t *testing.T) {
			env := newEnv(t)
			root := containerRoot(t, env, "app")

			res := env.RunWithEnv([]string{"BATON_CONTAINER_ROOT=" + root},
				bashPayload(t, "abc123", "curl -H 'Authorization: Bearer abc.DEF-123_456~xyz' -d token=hunter2 "+tokAnthropic, nil),
				"hook", "claude", "--profile", "app.claude", "--runtime", rt)

			if res.ExitCode != 0 || res.Stdout != "" || res.Stderr != "" {
				t.Fatalf("exit code = %d, stdout=%q, stderr=%q, want a silent success", res.ExitCode, res.Stdout, res.Stderr)
			}
			event := eventLines(t, env, ".baton/projects/app/sessions/claude-abc123.jsonl")[0]
			got := event["payload"].(map[string]any)["tool_input"].(map[string]any)["command"]
			if want := "curl -H 'Authorization: Bearer " + mask + "' -d token=" + mask + " " + mask; got != want {
				t.Errorf("command = %q, want %q", got, want)
			}
			if event["masked"] != true {
				t.Errorf("masked = %v, want true", event["masked"])
			}
		})
	}
}

func TestMask_AHandoffListsACommandThatCarriedASecretWithTheMask(t *testing.T) {
	env := newEnv(t)
	repo := handoffProjectRunning(t, env)
	writeSecrets(t, env, 0o600, "LITERAL=literalsecret1\n")
	in := bashPayload(t, "s1", "curl -H 'Authorization: Bearer abc.DEF-123_456~xyz' -d literalsecret1 "+tokGitLab, map[string]any{"cwd": repo})
	if res := env.RunWithStdin(in, hookArgs("claude-personal")...); res.ExitCode != 0 {
		t.Fatalf("hook: exit code = %d, stderr=%q", res.ExitCode, res.Stderr)
	}
	endFirstSession(t, env, repo)

	res := env.Run("handoff", handoffTask, "--to", "claude@claude-work")

	if res.ExitCode != 0 {
		t.Fatalf("exit code = %d, stderr=%q", res.ExitCode, res.Stderr)
	}
	got := env.ReadFile(handoffFile(env, "1"))
	want := "\n```\ncurl -H 'Authorization: Bearer " + mask + "' -d " + mask + " " + mask + "\n```\n"
	if !strings.Contains(got, want) {
		t.Errorf("handoff lacks the masked command:\n%s\nwant it to contain:\n%s", got, want)
	}
	for _, secret := range []string{"abc.DEF-123_456~xyz", "literalsecret1", tokGitLab} {
		if strings.Contains(got, secret) {
			t.Errorf("handoff holds a secret: %s", secret)
		}
	}
}

// recordedPayload runs the hook for a payload given as JSON text and returns
// the payload as the Event file holds it, and the Event.
func recordedPayload(t *testing.T, env *batontest.Env, in string) (map[string]any, map[string]any) {
	t.Helper()
	res := env.RunWithStdin(in, hookArgs("claude-personal")...)
	if res.ExitCode != 0 || res.Stdout != "" || res.Stderr != "" {
		t.Fatalf("exit code = %d, stdout=%q, stderr=%q, want a silent success", res.ExitCode, res.Stdout, res.Stderr)
	}
	lines := eventLines(t, env, ".baton/sessions/claude-abc123.jsonl")
	event := lines[len(lines)-1]
	return event["payload"].(map[string]any), event
}

func TestMask_AStringValueUnderAKeyThatSaysSecretGoesWholeInAnyCase(t *testing.T) {
	keys := []string{
		"token", "access_TOKEN", "Secret", "client_secret", "PASSWORD", "db_passwd",
		"api_key", "x-api-key", "X-Api-Key", "apikey", "APIKEY", "private_key", "Private-Key", "token_type",
	}
	for _, key := range keys {
		t.Run(key, func(t *testing.T) {
			env := newEnv(t)
			in := `{"session_id": "abc123", "hook_event_name": "PostToolUse", "cwd": "/x", "` + key + `": "plain value", "keep": "plain value"}`

			payload, event := recordedPayload(t, env, in)

			if payload[key] != mask || payload["keep"] != "plain value" {
				t.Errorf("payload = %#v, want %q under %q and the other value as it was", payload, mask, key)
			}
			if event["masked"] != true {
				t.Errorf("masked = %v, want true", event["masked"])
			}
		})
	}
}

func TestMask_TheKeyRuleReachesObjectsInsideArraysAndKeepsTheStructure(t *testing.T) {
	env := newEnv(t)
	in := `{
  "session_id": "abc123",
  "hook_event_name": "PostToolUse",
  "cwd": "/x",
  "tool_input": {"servers": [{"name": "a", "env": {"API_KEY": "abc"}}, {"name": "b", "Password": "hunter2"}], "list": ["plain", ["x"]]},
  "secrets": {"first": "one", "nested": {"second": "two", "count": 3}, "list": ["three", 4], "empty": ""}
}`

	payload, event := recordedPayload(t, env, in)

	want := map[string]any{
		"session_id":      "abc123",
		"hook_event_name": "PostToolUse",
		"cwd":             "/x",
		"tool_input": map[string]any{
			"servers": []any{
				map[string]any{"name": "a", "env": map[string]any{"API_KEY": mask}},
				map[string]any{"name": "b", "Password": mask},
			},
			"list": []any{"plain", []any{"x"}},
		},
		"secrets": map[string]any{
			"first":  "one",
			"nested": map[string]any{"second": "two", "count": float64(3)},
			"list":   []any{"three", float64(4)},
			"empty":  "",
		},
	}
	if !reflect.DeepEqual(payload, want) {
		t.Errorf("payload = %#v, want %#v: the object under secrets keeps its structure and its leaves, only matched strings go", payload, want)
	}
	if event["masked"] != true {
		t.Errorf("masked = %v, want true", event["masked"])
	}
}

func TestMask_OnlyANonEmptyStringIsReplacedByItsKeyNotNumbersBooleansNullEmptyStringsOrKeys(t *testing.T) {
	env := newEnv(t)
	in := `{"session_id": "abc123", "hook_event_name": "PostToolUse", "cwd": "/x", "max_tokens": 4096, "token_count": 12, "token_ok": true, "token_none": null, "password": "", "token_obj": {"id": "abc", "password": "p"}, "token_list": ["a", 1, {"api_key": "k"}]}`

	payload, event := recordedPayload(t, env, in)

	want := map[string]any{
		"session_id": "abc123", "hook_event_name": "PostToolUse", "cwd": "/x",
		"max_tokens": float64(4096), "token_count": float64(12), "token_ok": true, "token_none": nil,
		"password":   "",
		"token_obj":  map[string]any{"id": "abc", "password": mask},
		"token_list": []any{"a", float64(1), map[string]any{"api_key": mask}},
	}
	if !reflect.DeepEqual(payload, want) {
		t.Errorf("payload = %#v, want %#v", payload, want)
	}
	if event["masked"] != true {
		t.Errorf("masked = %v, want true", event["masked"])
	}
}

func TestMask_AStringLeafUnderAKeyThatSaysSecretIsStillMaskedByValue(t *testing.T) {
	env := newEnv(t)
	in := `{"session_id": "abc123", "hook_event_name": "PostToolUse", "cwd": "/x", "tokens": ["` + tokGitLab + `", "plain"], "note": "` + tokGitLab + `"}`

	payload, _ := recordedPayload(t, env, in)

	if got := payload["tokens"]; !reflect.DeepEqual(got, []any{mask, "plain"}) {
		t.Errorf("tokens = %#v, want the array kept and the token in it masked", got)
	}
	if payload["note"] != mask {
		t.Errorf("note = %v, want %q", payload["note"], mask)
	}
}

func TestMask_KeysAndTheFieldsTheEventKeepsAreNotChangedByTheKeyRule(t *testing.T) {
	env := newEnv(t)
	in := `{"session_id": "abc123", "hook_event_name": "PostToolUse", "cwd": "/work/token", "password": "hunter2"}`

	payload, event := recordedPayload(t, env, in)

	if _, ok := payload["password"]; !ok || payload["password"] != mask {
		t.Errorf("payload = %#v, want the key password kept with the mask", payload)
	}
	if event["cwd"] != "/work/token" || event["session_id"] != "abc123" {
		t.Errorf("cwd = %v, session_id = %v, want the two outside the payload untouched", event["cwd"], event["session_id"])
	}
}

func TestMask_APayloadWithSecretLookingKeysButNoStringValueToMaskIsWrittenAsItCame(t *testing.T) {
	env := newEnv(t)
	in := `{"session_id": "abc123", "zeta": 1, "hook_event_name": "Stop", "max_tokens": 4096, "alpha": 1.50, "password": "", "token": null, "cwd": "/x"}`

	res := env.RunWithStdin(in, hookArgs("claude-personal")...)

	if res.ExitCode != 0 {
		t.Fatalf("exit code = %d, stderr=%q", res.ExitCode, res.Stderr)
	}
	text := env.ReadFile(".baton/sessions/claude-abc123.jsonl")
	wantPayload := `"payload":{"session_id":"abc123","zeta":1,"hook_event_name":"Stop","max_tokens":4096,"alpha":1.50,"password":"","token":null,"cwd":"/x"}`
	if !strings.Contains(text, wantPayload) {
		t.Errorf("Event line = %q, want it to contain %s", text, wantPayload)
	}
	if strings.Contains(text, `"masked"`) {
		t.Errorf("Event line = %q, want no masked field", text)
	}
}
