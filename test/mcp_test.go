package test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/loadkpi/baton/internal/batontest"
)

// secretsPath is where Baton reads the values of ${VAR} placeholders.
const secretsRel = ".config/baton/secrets.env"

// writeSecrets writes the secrets file with the given mode.
func writeSecrets(t *testing.T, env *batontest.Env, mode os.FileMode, content string) {
	t.Helper()
	env.WriteFile(secretsRel, content)
	if err := os.Chmod(env.Path(secretsRel), mode); err != nil {
		t.Fatal(err)
	}
}

// writeClaudeJSON writes a MCP configuration the way Claude does, readable by
// its owner only; Baton refuses to put secrets into any other.
func writeClaudeJSON(t *testing.T, env *batontest.Env, path, content string) {
	t.Helper()
	env.WriteFile(path, content)
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
}

// sentryMCP is a source MCP manifest with one HTTP server whose header holds a
// placeholder.
const sentryMCP = `{
  "mcpServers": {
    "sentry": {
      "type": "http",
      "url": "https://sentry.example.test/mcp",
      "headers": { "Authorization": "Bearer ${SENTRY_TOKEN}" }
    }
  }
}
`

// withMCP sets up the MCP tests: the source holds the given mcp.json, the
// secrets file holds the given values (mode 0600), and the Profile exists. It
// returns the Profile.
func withMCP(t *testing.T, env *batontest.Env, mcp, secrets string) string {
	t.Helper()
	env.SourceFile("mcp.json", mcp)
	env.CommitSource("add mcp")
	profile := env.MakeProfileDir(".claude-personal")
	env.WriteConfig(standardConfig(env))
	writeSecrets(t, env, 0o600, secrets)
	return profile
}

func TestMCP_RendersServersWithSecretsIntoANewMCPConfiguration(t *testing.T) {
	env := newEnv(t)
	profile := withMCP(t, env, sentryMCP, "SENTRY_TOKEN=s3cret-value\n")

	res := env.Run("sync")

	if res.ExitCode != 0 {
		t.Fatalf("exit code = %d, stderr=%q", res.ExitCode, res.Stderr)
	}
	want := `{
  "mcpServers": {
    "sentry": {
      "headers": {
        "Authorization": "Bearer s3cret-value"
      },
      "type": "http",
      "url": "https://sentry.example.test/mcp"
    }
  }
}
`
	mcpFile := filepath.Join(profile, ".claude.json")
	if got := env.ReadFile(mcpFile); got != want {
		t.Errorf(".claude.json = %q, want %q", got, want)
	}
	if mode := env.Mode(mcpFile); mode != 0o600 {
		t.Errorf(".claude.json mode = %v, want 0600 because it holds secrets", mode)
	}
}

func TestMCP_MissingVariableStopsSyncNamingTheVariableAndTheServerAndWritesNothing(t *testing.T) {
	env := newEnv(t)
	profile := withMCP(t, env, sentryMCP, "OTHER=1\n")
	env.SourceFile("skills/greet/SKILL.md", "Hello.\n")
	env.CommitSource("add skill")

	res := env.Run("sync")

	if res.ExitCode == 0 {
		t.Fatalf("exit code = 0, want non-zero; stdout=%q", res.Stdout)
	}
	for _, want := range []string{"SENTRY_TOKEN", "sentry"} {
		if !strings.Contains(res.Stderr, want) {
			t.Errorf("stderr = %q, want it to name %q", res.Stderr, want)
		}
	}
	if env.Exists(filepath.Join(profile, ".claude.json")) || env.Exists(filepath.Join(profile, "skills")) || env.Exists(".baton") {
		t.Error("sync wrote something although a variable is missing")
	}
}

func TestMCP_SecretsFileReadableByOthersMakesSyncRefuseToRun(t *testing.T) {
	for _, mode := range []os.FileMode{0o644, 0o640, 0o604} {
		t.Run(mode.String(), func(t *testing.T) {
			env := newEnv(t)
			profile := withMCP(t, env, sentryMCP, "SENTRY_TOKEN=s3cret-value\n")
			env.SourceFile("skills/greet/SKILL.md", "Hello.\n")
			env.CommitSource("add skill")
			writeSecrets(t, env, mode, "SENTRY_TOKEN=s3cret-value\n")

			res := env.Run("sync")

			if res.ExitCode == 0 {
				t.Fatalf("exit code = 0, want non-zero; stdout=%q", res.Stdout)
			}
			if !strings.Contains(res.Stderr, env.Path(secretsRel)) || !strings.Contains(res.Stderr, "chmod 600") {
				t.Errorf("stderr = %q, want it to name the secrets file and say how to fix it", res.Stderr)
			}
			if strings.Contains(res.Stderr+res.Stdout, "s3cret-value") {
				t.Error("a secret value was printed")
			}
			if env.Exists(filepath.Join(profile, ".claude.json")) || env.Exists(filepath.Join(profile, "skills")) || env.Exists(".baton") {
				t.Error("sync wrote something although the secrets file is readable by others")
			}
		})
	}
}

func TestMCP_SecretsFileReadableByOthersIsRefusedEvenWhenNothingUsesIt(t *testing.T) {
	env := newEnv(t)
	env.SourceFile("skills/greet/SKILL.md", "Hello.\n")
	env.CommitSource("add skill")
	profile := env.MakeProfileDir(".claude-personal")
	env.WriteConfig(standardConfig(env))
	writeSecrets(t, env, 0o644, "UNUSED=1\n")

	res := env.Run("sync")

	if res.ExitCode == 0 {
		t.Fatalf("exit code = 0, want non-zero; stdout=%q", res.Stdout)
	}
	if env.Exists(filepath.Join(profile, "skills")) {
		t.Error("sync wrote skills although the secrets file is readable by others")
	}
}

// manifestKeys returns the owned keys recorded in a Profile's manifest, as
// "path key.path" to checksum.
func manifestKeys(t *testing.T, env *batontest.Env, profile string) map[string]string {
	t.Helper()
	var m struct {
		Keys []struct {
			Path   string   `json:"path"`
			Key    []string `json:"key"`
			Item   string   `json:"item"`
			SHA256 string   `json:"sha256"`
		} `json:"keys"`
	}
	if err := json.Unmarshal([]byte(env.ReadFile(".baton/manifests/"+profile+".json")), &m); err != nil {
		t.Fatalf("manifest is not valid JSON: %v", err)
	}
	keys := make(map[string]string)
	for _, k := range m.Keys {
		if k.Item != "" {
			continue // an entry of a shared array: a hook, see manifestHooks
		}
		keys[k.Path+" "+strings.Join(k.Key, ".")] = k.SHA256
	}
	return keys
}

func TestMCP_ManifestRecordsOwnedKeysWithTheChecksumOfTheirValue(t *testing.T) {
	env := newEnv(t)
	withMCP(t, env, sentryMCP, "SENTRY_TOKEN=s3cret-value\n")

	res := env.Run("sync")

	if res.ExitCode != 0 {
		t.Fatalf("exit code = %d, stderr=%q", res.ExitCode, res.Stderr)
	}
	// sha256 of the compact JSON of the server with keys sorted and the secret
	// substituted, worked out with sha256sum.
	want := map[string]string{
		".claude.json mcpServers.sentry": "22ef0f5d3c356827ba02928bcf010dc6d6efe731db101ac4b2cba2c0711bd211",
	}
	if got := manifestKeys(t, env, "claude-personal"); !reflect.DeepEqual(got, want) {
		t.Errorf("manifest keys = %v, want %v", got, want)
	}
}

// existingClaudeJSON is a Claude MCP configuration that belongs to the user:
// state of Claude itself (with a made-up token that must never be printed) and
// a server of their own, indented with tabs.
const existingClaudeJSON = "{\n" +
	"\t\"numStartups\": 12,\n" +
	"\t\"oauthAccount\": {\"accessToken\": \"tok-claude-account\"},\n" +
	"\t\"mcpServers\": {\n" +
	"\t\t\"mine\": {\"command\": \"my-server\"}\n" +
	"\t}\n" +
	"}\n"

func TestMCP_ExistingMCPConfigurationWithoutTerminalStopsAndWritesNothing(t *testing.T) {
	env := newEnv(t)
	profile := withMCP(t, env, sentryMCP, "SENTRY_TOKEN=s3cret-value\n")
	env.SourceFile("skills/greet/SKILL.md", "Hello.\n")
	env.CommitSource("add skill")
	mcpFile := filepath.Join(profile, ".claude.json")
	writeClaudeJSON(t, env, mcpFile, existingClaudeJSON)

	res := env.RunWithStdin("y\n", "sync")

	if res.ExitCode == 0 {
		t.Fatalf("exit code = 0, want non-zero; stdout=%q", res.Stdout)
	}
	if !strings.Contains(res.Stderr, mcpFile) || !strings.Contains(res.Stderr, "terminal") {
		t.Errorf("stderr = %q, want it to name %s and say there is no terminal", res.Stderr, mcpFile)
	}
	if strings.Contains(res.Stderr+res.Stdout, "s3cret-value") {
		t.Error("a secret value was printed")
	}
	if got := env.ReadFile(mcpFile); got != existingClaudeJSON {
		t.Errorf(".claude.json = %q, want it untouched", got)
	}
	if env.Exists(filepath.Join(profile, "skills")) || env.Exists(".baton") {
		t.Error("sync wrote something although it could not ask")
	}
}

func TestMCP_AddsServersToAnExistingConfigurationAfterBackupKeepingEveryOtherByte(t *testing.T) {
	env := newEnv(t)
	profile := withMCP(t, env, sentryMCP, "SENTRY_TOKEN=s3cret-value\n")
	mcpFile := filepath.Join(profile, ".claude.json")
	writeClaudeJSON(t, env, mcpFile, existingClaudeJSON)

	res := env.RunTerminal([]string{"y"}, "sync")

	if res.ExitCode != 0 {
		t.Fatalf("exit code = %d, transcript:\n%s", res.ExitCode, res.Stdout)
	}
	want := "{\n" +
		"\t\"numStartups\": 12,\n" +
		"\t\"oauthAccount\": {\"accessToken\": \"tok-claude-account\"},\n" +
		"\t\"mcpServers\": {\n" +
		"\t\t\"mine\": {\"command\": \"my-server\"},\n" +
		"\t\t\"sentry\": {\n" +
		"\t\t\t\"headers\": {\n" +
		"\t\t\t\t\"Authorization\": \"Bearer s3cret-value\"\n" +
		"\t\t\t},\n" +
		"\t\t\t\"type\": \"http\",\n" +
		"\t\t\t\"url\": \"https://sentry.example.test/mcp\"\n" +
		"\t\t}\n" +
		"\t}\n" +
		"}\n"
	if got := env.ReadFile(mcpFile); got != want {
		t.Errorf(".claude.json = %q, want %q", got, want)
	}
	if got := env.ReadFile(filepath.Join(onlyBackup(t, env, "claude-personal"), ".claude.json")); got != existingClaudeJSON {
		t.Errorf("backed up .claude.json = %q, want the original", got)
	}
}

func TestMCP_TheQuestionShowsOnlyBatonsKeysAndNeverASecretValue(t *testing.T) {
	env := newEnv(t)
	profile := withMCP(t, env, sentryMCP, "SENTRY_TOKEN=s3cret-value\n")
	mcpFile := filepath.Join(profile, ".claude.json")
	writeClaudeJSON(t, env, mcpFile, existingClaudeJSON)

	res := env.RunTerminal([]string{"y"}, "sync")

	if res.ExitCode != 0 {
		t.Fatalf("exit code = %d, transcript:\n%s", res.ExitCode, res.Stdout)
	}
	for _, want := range []string{
		mcpFile,
		"+mcpServers.sentry = {",
		`+    "Authorization": "Bearer ${SENTRY_TOKEN}"`,
	} {
		if !strings.Contains(res.Stdout, want) {
			t.Errorf("transcript lacks %q:\n%s", want, res.Stdout)
		}
	}
	for _, secret := range []string{"s3cret-value", "tok-claude-account"} {
		if strings.Contains(res.Stdout, secret) {
			t.Errorf("transcript shows %q:\n%s", secret, res.Stdout)
		}
	}
}

func TestMCP_AnsweringNoLeavesTheMCPConfigurationAlone(t *testing.T) {
	env := newEnv(t)
	profile := withMCP(t, env, sentryMCP, "SENTRY_TOKEN=s3cret-value\n")
	mcpFile := filepath.Join(profile, ".claude.json")
	writeClaudeJSON(t, env, mcpFile, existingClaudeJSON)

	res := env.RunTerminal([]string{"n"}, "sync")

	if res.ExitCode == 0 {
		t.Fatalf("exit code = 0, want non-zero; transcript:\n%s", res.Stdout)
	}
	if got := env.ReadFile(mcpFile); got != existingClaudeJSON {
		t.Errorf(".claude.json = %q, want it untouched", got)
	}
	if env.Exists(".baton/manifests") || env.Exists(".baton/backups") {
		t.Error("sync wrote something after the answer no")
	}
}

func TestMCP_SecondSyncWithoutChangesAsksNothingAndRewritesNothing(t *testing.T) {
	env := newEnv(t)
	profile := withMCP(t, env, sentryMCP, "SENTRY_TOKEN=s3cret-value\n")
	mcpFile := filepath.Join(profile, ".claude.json")
	writeClaudeJSON(t, env, mcpFile, existingClaudeJSON)
	if res := env.RunTerminal([]string{"y"}, "sync"); res.ExitCode != 0 {
		t.Fatalf("first sync: exit code = %d, transcript:\n%s", res.ExitCode, res.Stdout)
	}
	before := env.ReadFile(mcpFile)
	modTime := env.ModTime(mcpFile)

	res := env.Run("sync")

	if res.ExitCode != 0 {
		t.Fatalf("second sync: exit code = %d, stderr=%q", res.ExitCode, res.Stderr)
	}
	if got := env.ReadFile(mcpFile); got != before {
		t.Errorf(".claude.json = %q, want it unchanged", got)
	}
	if !env.ModTime(mcpFile).Equal(modTime) {
		t.Error("the second sync rewrote .claude.json")
	}
}

// afterFirstSync runs the first sync over existingClaudeJSON, answering yes.
func afterFirstSync(t *testing.T, env *batontest.Env, profile string) string {
	t.Helper()
	mcpFile := filepath.Join(profile, ".claude.json")
	writeClaudeJSON(t, env, mcpFile, existingClaudeJSON)
	if res := env.RunTerminal([]string{"y"}, "sync"); res.ExitCode != 0 {
		t.Fatalf("first sync: exit code = %d, transcript:\n%s", res.ExitCode, res.Stdout)
	}
	return mcpFile
}

func TestMCP_LaterSyncsUpdateTheirServersWithoutAskingAndKeepTheRestByteForByte(t *testing.T) {
	env := newEnv(t)
	profile := withMCP(t, env, sentryMCP, "SENTRY_TOKEN=s3cret-value\n")
	mcpFile := afterFirstSync(t, env, profile)
	env.SourceFile("mcp.json", strings.Replace(sentryMCP, "sentry.example.test", "sentry.example.test/v2", 1))
	writeSecrets(t, env, 0o600, "SENTRY_TOKEN=rotated-value\n")

	res := env.Run("sync")

	if res.ExitCode != 0 {
		t.Fatalf("exit code = %d, stderr=%q", res.ExitCode, res.Stderr)
	}
	want := "{\n" +
		"\t\"numStartups\": 12,\n" +
		"\t\"oauthAccount\": {\"accessToken\": \"tok-claude-account\"},\n" +
		"\t\"mcpServers\": {\n" +
		"\t\t\"mine\": {\"command\": \"my-server\"},\n" +
		"\t\t\"sentry\": {\n" +
		"\t\t\t\"headers\": {\n" +
		"\t\t\t\t\"Authorization\": \"Bearer rotated-value\"\n" +
		"\t\t\t},\n" +
		"\t\t\t\"type\": \"http\",\n" +
		"\t\t\t\"url\": \"https://sentry.example.test/v2/mcp\"\n" +
		"\t\t}\n" +
		"\t}\n" +
		"}\n"
	if got := env.ReadFile(mcpFile); got != want {
		t.Errorf(".claude.json = %q, want %q", got, want)
	}
	if got := len(env.ListDir(".baton/backups/claude-personal")); got != 1 {
		t.Errorf("%d backups, want only the one of the first sync", got)
	}
}

func TestMCP_AsksAgainWhenAServerWasEditedByHandAndStillHidesTheSecret(t *testing.T) {
	env := newEnv(t)
	profile := withMCP(t, env, sentryMCP, "SENTRY_TOKEN=s3cret-value\n")
	mcpFile := afterFirstSync(t, env, profile)
	edited := strings.Replace(env.ReadFile(mcpFile), `"type": "http"`, `"type": "sse"`, 1)
	writeClaudeJSON(t, env, mcpFile, edited)

	res := env.Run("sync")

	if res.ExitCode == 0 {
		t.Fatalf("exit code = 0, want non-zero; stdout=%q", res.Stdout)
	}
	if !strings.Contains(res.Stderr, mcpFile) || !strings.Contains(res.Stderr, "terminal") {
		t.Errorf("stderr = %q, want it to name %s and say there is no terminal", res.Stderr, mcpFile)
	}
	if got := env.ReadFile(mcpFile); got != edited {
		t.Errorf(".claude.json = %q, want the hand-edited version kept", got)
	}

	answered := env.RunTerminal([]string{"y"}, "sync")

	if answered.ExitCode != 0 {
		t.Fatalf("exit code = %d, transcript:\n%s", answered.ExitCode, answered.Stdout)
	}
	for _, want := range []string{`-  "type": "sse",`, `+  "type": "http",`, `    "Authorization": "Bearer ${SENTRY_TOKEN}"`} {
		if !strings.Contains(answered.Stdout, want) {
			t.Errorf("transcript lacks %q:\n%s", want, answered.Stdout)
		}
	}
	if strings.Contains(answered.Stdout, "s3cret-value") {
		t.Errorf("transcript shows the secret:\n%s", answered.Stdout)
	}
	if !strings.Contains(env.ReadFile(mcpFile), `"type": "http"`) {
		t.Errorf(".claude.json = %q, want Baton's server restored", env.ReadFile(mcpFile))
	}
	if got := len(env.ListDir(".baton/backups/claude-personal")); got != 2 {
		t.Errorf("%d backups, want 2", got)
	}
}

func TestMCP_AsksAgainWhenAServerWasCutOutByHand(t *testing.T) {
	env := newEnv(t)
	profile := withMCP(t, env, sentryMCP, "SENTRY_TOKEN=s3cret-value\n")
	mcpFile := afterFirstSync(t, env, profile)
	writeClaudeJSON(t, env, mcpFile, existingClaudeJSON)

	res := env.Run("sync")

	if res.ExitCode == 0 {
		t.Fatalf("exit code = 0, want non-zero; stdout=%q", res.Stdout)
	}
	if !strings.Contains(res.Stderr, mcpFile) {
		t.Errorf("stderr = %q, want it to name %s", res.Stderr, mcpFile)
	}
}

func TestMCP_RemovingAServerFromTheSourceRemovesOnlyItsKey(t *testing.T) {
	env := newEnv(t)
	profile := withMCP(t, env, sentryMCP, "SENTRY_TOKEN=s3cret-value\n")
	mcpFile := afterFirstSync(t, env, profile)
	env.SourceFile("mcp.json", `{"mcpServers": {}}`)

	res := env.Run("sync")

	if res.ExitCode != 0 {
		t.Fatalf("exit code = %d, stderr=%q", res.ExitCode, res.Stderr)
	}
	if got := env.ReadFile(mcpFile); got != existingClaudeJSON {
		t.Errorf(".claude.json = %q, want it as it was before Baton", got)
	}
	if got := manifestKeys(t, env, "claude-personal"); len(got) != 0 {
		t.Errorf("manifest keys = %v, want none", got)
	}
}

func TestMCP_TheQuestionForAHandEditedServerThatLeftTheSourceHidesItsSecretToo(t *testing.T) {
	env := newEnv(t)
	twoServers := `{"mcpServers": {
  "sentry": {"type": "http", "url": "https://sentry.example.test/mcp", "headers": {"Authorization": "Bearer ${SENTRY_TOKEN}"}},
  "grafana": {"type": "http", "url": "https://grafana.example.test/mcp", "headers": {"Authorization": "Bearer ${GRAFANA_TOKEN}"}}
}}`
	profile := withMCP(t, env, twoServers, "SENTRY_TOKEN=s3cret-sentry\nGRAFANA_TOKEN=s3cret-grafana\n")
	mcpFile := afterFirstSync(t, env, profile)
	env.SourceFile("mcp.json", sentryMCP)
	writeClaudeJSON(t, env, mcpFile, strings.Replace(env.ReadFile(mcpFile), `"type": "http",`+"\n\t\t\t\"url\": \"https://grafana", `"type": "sse",`+"\n\t\t\t\"url\": \"https://grafana", 1))

	res := env.RunTerminal([]string{"y"}, "sync")

	if res.ExitCode != 0 {
		t.Fatalf("exit code = %d, transcript:\n%s", res.ExitCode, res.Stdout)
	}
	if !strings.Contains(res.Stdout, "-mcpServers.grafana = {") || !strings.Contains(res.Stdout, "${GRAFANA_TOKEN}") {
		t.Errorf("transcript does not show the removed server:\n%s", res.Stdout)
	}
	for _, secret := range []string{"s3cret-sentry", "s3cret-grafana"} {
		if strings.Contains(res.Stdout, secret) {
			t.Errorf("transcript shows %q:\n%s", secret, res.Stdout)
		}
	}
	if strings.Contains(env.ReadFile(mcpFile), "grafana") {
		t.Errorf(".claude.json = %q, want the grafana server removed", env.ReadFile(mcpFile))
	}
}

const taggedMCP = `{
  "mcpServers": {
    "docs": { "type": "http", "url": "https://docs.example.test/mcp" },
    "tracker": {
      "tags": ["work"],
      "type": "http",
      "url": "https://tracker.example.test/mcp",
      "headers": { "Authorization": "Bearer ${TRACKER_TOKEN}" }
    }
  }
}
`

func TestMCP_ATaggedServerAndItsSecretReachOnlyProfilesThatAcceptItsTag(t *testing.T) {
	env := newEnv(t)
	env.SourceFile("mcp.json", taggedMCP)
	env.CommitSource("add mcp")
	personal := env.MakeProfileDir(".claude-personal")
	work := env.MakeProfileDir(".claude-work")
	env.WriteConfig(twoProfilesConfig(env, `["personal"]`, `["work"]`))
	writeSecrets(t, env, 0o600, "TRACKER_TOKEN=s3cret-tracker\n")

	res := env.Run("sync")

	if res.ExitCode != 0 {
		t.Fatalf("exit code = %d, stderr=%q", res.ExitCode, res.Stderr)
	}
	personalJSON := env.ReadFile(filepath.Join(personal, ".claude.json"))
	workJSON := env.ReadFile(filepath.Join(work, ".claude.json"))
	if !strings.Contains(personalJSON, `"docs"`) || !strings.Contains(workJSON, `"docs"`) {
		t.Errorf("the untagged server is missing: personal=%q work=%q", personalJSON, workJSON)
	}
	if strings.Contains(personalJSON, "tracker") || strings.Contains(personalJSON, "s3cret-tracker") {
		t.Errorf("the personal Profile received the work server or its secret: %q", personalJSON)
	}
	if !strings.Contains(workJSON, "Bearer s3cret-tracker") {
		t.Errorf("the work Profile lacks the tracker server: %q", workJSON)
	}
	if strings.Contains(workJSON, `"tags"`) {
		t.Errorf("the tags leaked into the Profile: %q", workJSON)
	}
	if got := manifestKeys(t, env, "claude-personal"); len(got) != 1 {
		t.Errorf("personal manifest keys = %v, want only the untagged server", got)
	}
}

func TestMCP_AVariableOfAServerNoProfileAcceptsIsNotNeeded(t *testing.T) {
	env := newEnv(t)
	env.SourceFile("mcp.json", taggedMCP)
	env.CommitSource("add mcp")
	personal := env.MakeProfileDir(".claude-personal")
	env.WriteConfig(`source = "` + env.SourceDir + `"

[profiles.claude-personal]
agent = "claude"
dir = "` + personal + `"
tags = ["personal"]
`)

	res := env.Run("sync")

	if res.ExitCode != 0 {
		t.Fatalf("exit code = %d, stderr=%q", res.ExitCode, res.Stderr)
	}
	if got := env.ReadFile(filepath.Join(personal, ".claude.json")); strings.Contains(got, "tracker") {
		t.Errorf(".claude.json = %q, want no work server", got)
	}
}

func TestMCP_ChangingAProfilesTagsRemovesTheServersItNoLongerAcceptsAndOnlyThose(t *testing.T) {
	env := newEnv(t)
	env.SourceFile("mcp.json", taggedMCP)
	env.CommitSource("add mcp")
	work := env.MakeProfileDir(".claude-work")
	env.MakeProfileDir(".claude-personal")
	env.WriteConfig(twoProfilesConfig(env, `["personal"]`, `["work"]`))
	writeSecrets(t, env, 0o600, "TRACKER_TOKEN=s3cret-tracker\n")
	if res := env.Run("sync"); res.ExitCode != 0 {
		t.Fatalf("first sync: exit code = %d, stderr=%q", res.ExitCode, res.Stderr)
	}
	env.WriteConfig(twoProfilesConfig(env, `["personal"]`, `["family"]`))

	res := env.Run("sync")

	if res.ExitCode != 0 {
		t.Fatalf("exit code = %d, stderr=%q", res.ExitCode, res.Stderr)
	}
	want := `{
  "mcpServers": {
    "docs": {
      "type": "http",
      "url": "https://docs.example.test/mcp"
    }
  }
}
`
	if got := env.ReadFile(filepath.Join(work, ".claude.json")); got != want {
		t.Errorf(".claude.json = %q, want %q", got, want)
	}
}

// syncOver runs a first sync (answering yes) over a Profile whose .claude.json
// holds before, and returns the file afterwards.
func syncOver(t *testing.T, before string) string {
	t.Helper()
	env := newEnv(t)
	profile := withMCP(t, env, sentryMCP, "SENTRY_TOKEN=s3cret-value\n")
	mcpFile := filepath.Join(profile, ".claude.json")
	writeClaudeJSON(t, env, mcpFile, before)
	if res := env.RunTerminal([]string{"y"}, "sync"); res.ExitCode != 0 {
		t.Fatalf("exit code = %d, transcript:\n%s", res.ExitCode, res.Stdout)
	}
	return env.ReadFile(mcpFile)
}

// sentryAt is the sentry server as Baton writes it at a nesting depth, with
// the given indentation unit and line start.
func sentryAt(indent, unit string) string {
	return "{\n" +
		indent + unit + "\"headers\": {\n" +
		indent + unit + unit + "\"Authorization\": \"Bearer s3cret-value\"\n" +
		indent + unit + "},\n" +
		indent + unit + "\"type\": \"http\",\n" +
		indent + unit + "\"url\": \"https://sentry.example.test/mcp\"\n" +
		indent + "}"
}

func TestMCP_EditorFormats(t *testing.T) {
	tests := []struct{ name, before, want string }{
		{
			name:   "empty document",
			before: "{}\n",
			want:   "{\n  \"mcpServers\": {\n    \"sentry\": " + sentryAt("    ", "  ") + "\n  }\n}\n",
		},
		{
			name:   "no mcpServers key, four-space indentation",
			before: "{\n    \"numStartups\": 12\n}\n",
			want:   "{\n    \"numStartups\": 12,\n    \"mcpServers\": {\n        \"sentry\": " + sentryAt("        ", "    ") + "\n    }\n}\n",
		},
		{
			name:   "empty mcpServers",
			before: "{\n  \"mcpServers\": {},\n  \"theme\": \"dark\"\n}\n",
			want:   "{\n  \"mcpServers\": {\n    \"sentry\": " + sentryAt("    ", "  ") + "\n  },\n  \"theme\": \"dark\"\n}\n",
		},
		{
			name:   "document on one line",
			before: `{"numStartups":12,"mcpServers":{"mine":{"command":"x"}}}` + "\n",
			want:   `{"numStartups":12,"mcpServers":{"mine":{"command":"x"},"sentry":{"headers":{"Authorization":"Bearer s3cret-value"},"type":"http","url":"https://sentry.example.test/mcp"}}}` + "\n",
		},
		{
			name:   "no newline at the end and exotic values elsewhere",
			before: "{\n  \"big\": 12345678901234567890.5e3,\n  \"s\": \"\\u00e9\\n\",\n  \"mcpServers\": {\n    \"mine\": {}\n  }\n}",
			want:   "{\n  \"big\": 12345678901234567890.5e3,\n  \"s\": \"\\u00e9\\n\",\n  \"mcpServers\": {\n    \"mine\": {},\n    \"sentry\": " + sentryAt("    ", "  ") + "\n  }\n}",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := syncOver(t, tt.before); got != tt.want {
				t.Errorf(".claude.json = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestMCP_UpdatingAndRemovingServersInADocumentOnOneLineKeepsItOnOneLine(t *testing.T) {
	env := newEnv(t)
	profile := withMCP(t, env, sentryMCP, "SENTRY_TOKEN=s3cret-value\n")
	mcpFile := filepath.Join(profile, ".claude.json")
	writeClaudeJSON(t, env, mcpFile, `{"a": 1, "mcpServers": {"mine": {"command": "x"}}}`+"\n")
	if res := env.RunTerminal([]string{"y"}, "sync"); res.ExitCode != 0 {
		t.Fatalf("first sync: exit code = %d, transcript:\n%s", res.ExitCode, res.Stdout)
	}
	env.SourceFile("mcp.json", strings.Replace(sentryMCP, "sentry.example.test", "sentry.example.test/v2", 1))

	if res := env.Run("sync"); res.ExitCode != 0 {
		t.Fatalf("update: exit code = %d, stderr=%q", res.ExitCode, res.Stderr)
	}

	want := `{"a": 1, "mcpServers": {"mine": {"command": "x"}, "sentry": {"headers":{"Authorization":"Bearer s3cret-value"},"type":"http","url":"https://sentry.example.test/v2/mcp"}}}` + "\n"
	if got := env.ReadFile(mcpFile); got != want {
		t.Errorf("after the update .claude.json = %q, want %q", got, want)
	}

	env.SourceFile("mcp.json", `{"mcpServers": {}}`)
	if res := env.Run("sync"); res.ExitCode != 0 {
		t.Fatalf("removal: exit code = %d, stderr=%q", res.ExitCode, res.Stderr)
	}
	if got, want := env.ReadFile(mcpFile), `{"a": 1, "mcpServers": {"mine": {"command": "x"}}}`+"\n"; got != want {
		t.Errorf("after the removal .claude.json = %q, want %q", got, want)
	}
}

func TestMCP_RemovingAServerAmongOthersTakesOutOnlyItself(t *testing.T) {
	tests := []struct{ name, before, want string }{
		{
			name:   "first of two",
			before: "{\n  \"mcpServers\": {\n    \"sentry\": " + sentryAt("    ", "  ") + ",\n    \"zed\": {\"command\": \"z\"}\n  }\n}\n",
			want:   "{\n  \"mcpServers\": {\n    \"zed\": {\"command\": \"z\"}\n  }\n}\n",
		},
		{
			name:   "middle of three",
			before: "{\n  \"mcpServers\": {\n    \"a\": {},\n    \"sentry\": " + sentryAt("    ", "  ") + ",\n    \"zed\": {\"command\": \"z\"}\n  }\n}\n",
			want:   "{\n  \"mcpServers\": {\n    \"a\": {},\n    \"zed\": {\"command\": \"z\"}\n  }\n}\n",
		},
		{
			name:   "only one",
			before: "{\n  \"mcpServers\": {\n    \"sentry\": " + sentryAt("    ", "  ") + "\n  }\n}\n",
			want:   "{\n  \"mcpServers\": {}\n}\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := newEnv(t)
			profile := withMCP(t, env, sentryMCP, "SENTRY_TOKEN=s3cret-value\n")
			mcpFile := filepath.Join(profile, ".claude.json")
			if res := env.Run("sync"); res.ExitCode != 0 { // records the key; the file is Baton's own
				t.Fatalf("first sync: exit code = %d, stderr=%q", res.ExitCode, res.Stderr)
			}
			writeClaudeJSON(t, env, mcpFile, tt.before)
			env.SourceFile("mcp.json", `{"mcpServers": {}}`)

			res := env.Run("sync")

			if res.ExitCode != 0 {
				t.Fatalf("exit code = %d, stderr=%q", res.ExitCode, res.Stderr)
			}
			if got := env.ReadFile(mcpFile); got != tt.want {
				t.Errorf(".claude.json = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestMCP_AMCPConfigurationThatCannotBeEditedStopsSyncNamingTheFileAndWritesNothing(t *testing.T) {
	tests := []struct{ name, content string }{
		{"not JSON", `{"mcpServers": {"mine": }}` + "\n"},
		{"cut off", `{"mcpServers": {`},
		{"top level is a list", "[]\n"},
		{"mcpServers is not an object", `{"mcpServers": null}` + "\n"},
		{"mcpServers is a list", `{"mcpServers": []}` + "\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := newEnv(t)
			profile := withMCP(t, env, sentryMCP, "SENTRY_TOKEN=s3cret-value\n")
			mcpFile := filepath.Join(profile, ".claude.json")
			writeClaudeJSON(t, env, mcpFile, tt.content)

			res := env.Run("sync")

			if res.ExitCode == 0 {
				t.Fatalf("exit code = 0, want non-zero; stdout=%q", res.Stdout)
			}
			if !strings.Contains(res.Stderr, mcpFile) {
				t.Errorf("transcript = %q, want it to name %s", res.Stderr, mcpFile)
			}
			if strings.Contains(res.Stderr, "panic") || strings.Contains(res.Stderr+res.Stdout, "s3cret-value") {
				t.Errorf("stderr = %q", res.Stderr)
			}
			if got := env.ReadFile(mcpFile); got != tt.content {
				t.Errorf(".claude.json = %q, want it untouched", got)
			}
			if env.Exists(".baton") {
				t.Error("sync wrote something")
			}
		})
	}
}

func TestMCP_AnUnreadableMCPManifestStopsSyncNamingTheFileAndTheServer(t *testing.T) {
	tests := []struct{ name, manifest, mention string }{
		{"not JSON", `{"mcpServers": {`, "mcp.json"},
		{"server is not an object", `{"mcpServers": {"sentry": "https://x"}}`, "mcp.json"},
		{"tags is not a list", `{"mcpServers": {"sentry": {"tags": "work", "type": "http"}}}`, `"sentry"`},
		{"tags is empty", `{"mcpServers": {"sentry": {"tags": [], "type": "http"}}}`, `"sentry"`},
		{"tag is invalid", `{"mcpServers": {"sentry": {"tags": ["Work!"], "type": "http"}}}`, `"sentry"`},
		{"unknown top-level member", `{"servers": {}}`, "servers"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := newEnv(t)
			profile := withMCP(t, env, tt.manifest, "SENTRY_TOKEN=s3cret-value\n")
			env.SourceFile("skills/greet/SKILL.md", "Hello.\n")
			env.CommitSource("add skill")

			res := env.Run("sync")

			if res.ExitCode == 0 {
				t.Fatalf("exit code = 0, want non-zero; stdout=%q", res.Stdout)
			}
			if !strings.Contains(res.Stderr, "mcp.json") || !strings.Contains(res.Stderr, tt.mention) {
				t.Errorf("stderr = %q, want it to name mcp.json and %s", res.Stderr, tt.mention)
			}
			if env.Exists(filepath.Join(profile, ".claude.json")) || env.Exists(filepath.Join(profile, "skills")) || env.Exists(".baton") {
				t.Error("sync wrote something")
			}
		})
	}
}

func TestMCP_SecretsFileFormat(t *testing.T) {
	env := newEnv(t)
	manifest := `{"mcpServers": {"x": {"command": "x", "args": ["${A}", "${B}", "${C}", "${D}", "${E}"], "env": {"K": "${A}-${B}"}}}}`
	profile := withMCP(t, env, manifest, "# tokens\n\nA=plain\nB=\"quoted value\"\nC='single'\nD=has=equals\nE=\n")

	res := env.Run("sync")

	if res.ExitCode != 0 {
		t.Fatalf("exit code = %d, stderr=%q", res.ExitCode, res.Stderr)
	}
	want := `{
  "mcpServers": {
    "x": {
      "args": [
        "plain",
        "quoted value",
        "single",
        "has=equals",
        ""
      ],
      "command": "x",
      "env": {
        "K": "plain-quoted value"
      }
    }
  }
}
`
	if got := env.ReadFile(filepath.Join(profile, ".claude.json")); got != want {
		t.Errorf(".claude.json = %q, want %q", got, want)
	}
}

func TestMCP_AMalformedSecretsLineIsReportedByNumberAndNeverShown(t *testing.T) {
	env := newEnv(t)
	profile := withMCP(t, env, sentryMCP, "SENTRY_TOKEN=fine\nhunter2-not-a-pair\n")

	res := env.Run("sync")

	if res.ExitCode == 0 {
		t.Fatalf("exit code = 0, want non-zero; stdout=%q", res.Stdout)
	}
	if !strings.Contains(res.Stderr, env.Path(secretsRel)+":2") {
		t.Errorf("stderr = %q, want it to name the file and line 2", res.Stderr)
	}
	if strings.Contains(res.Stderr, "hunter2") {
		t.Errorf("stderr = %q shows the line", res.Stderr)
	}
	if env.Exists(filepath.Join(profile, ".claude.json")) {
		t.Error("sync wrote something")
	}
}

func TestMCP_NeverWritesAnMCPConfigurationThatGitTracks(t *testing.T) {
	env := newEnv(t)
	profile := withMCP(t, env, sentryMCP, "SENTRY_TOKEN=s3cret-value\n")
	mcpFile := filepath.Join(profile, ".claude.json")
	writeClaudeJSON(t, env, mcpFile, existingClaudeJSON)
	gitInit(t, env, profile)

	res := env.RunTerminal(nil, "sync")

	if res.ExitCode == 0 {
		t.Fatalf("exit code = 0, want non-zero; transcript:\n%s", res.Stdout)
	}
	if !strings.Contains(res.Stdout, mcpFile) || !strings.Contains(res.Stdout, "git") {
		t.Errorf("transcript = %q, want an error that names %s and git", res.Stdout, mcpFile)
	}
	if got := env.ReadFile(mcpFile); got != existingClaudeJSON {
		t.Errorf(".claude.json = %q, want it untouched", got)
	}
}

func TestMCP_AnMCPConfigurationThatIsASymlinkKeepsTheLinkAndChangesItsTarget(t *testing.T) {
	env := newEnv(t)
	profile := withMCP(t, env, sentryMCP, "SENTRY_TOKEN=s3cret-value\n")
	shared := env.Path("dotfiles/claude.json")
	writeClaudeJSON(t, env, shared, "{}\n")
	if err := os.Symlink(shared, filepath.Join(profile, ".claude.json")); err != nil {
		t.Fatal(err)
	}

	res := env.RunTerminal([]string{"y"}, "sync")

	if res.ExitCode != 0 {
		t.Fatalf("exit code = %d, transcript:\n%s", res.ExitCode, res.Stdout)
	}
	if target, err := os.Readlink(filepath.Join(profile, ".claude.json")); err != nil || target != shared {
		t.Errorf(".claude.json link = %q (err %v), want it kept, pointing to %s", target, err, shared)
	}
	if got := env.ReadFile(shared); !strings.Contains(got, "Bearer s3cret-value") {
		t.Errorf("link target = %q, want Baton's server in it", got)
	}
}

func TestMCP_AMissingVariableOfOneProfileStopsSyncBeforeAnyProfileIsWritten(t *testing.T) {
	env := newEnv(t)
	env.SourceFile("mcp.json", taggedMCP)
	env.SourceFile("skills/greet/SKILL.md", "Hello.\n")
	env.CommitSource("add mcp")
	personal := env.MakeProfileDir(".claude-personal")
	env.MakeProfileDir(".claude-work")
	env.WriteConfig(twoProfilesConfig(env, `["personal"]`, `["work"]`))

	res := env.Run("sync")

	if res.ExitCode == 0 {
		t.Fatalf("exit code = 0, want non-zero; stdout=%q", res.Stdout)
	}
	if !strings.Contains(res.Stderr, "TRACKER_TOKEN") || !strings.Contains(res.Stderr, "tracker") {
		t.Errorf("stderr = %q, want it to name the variable and the server", res.Stderr)
	}
	if env.Exists(filepath.Join(personal, ".claude.json")) || env.Exists(filepath.Join(personal, "skills")) || env.Exists(".baton") {
		t.Error("the personal Profile was written although the work Profile could not be planned")
	}
}

func TestMCP_TheQuestionHidesWhateverSitsWhereASecretGoes(t *testing.T) {
	env := newEnv(t)
	profile := withMCP(t, env, sentryMCP, "SENTRY_TOKEN=old-token-value\n")
	mcpFile := afterFirstSync(t, env, profile)
	// The token was rotated in the secrets file, so the old one is no longer
	// known to Baton, and the server was edited by hand.
	writeSecrets(t, env, 0o600, "SENTRY_TOKEN=new-token-value\n")
	writeClaudeJSON(t, env, mcpFile, strings.Replace(env.ReadFile(mcpFile), `"type": "http"`, `"type": "sse"`, 1))

	res := env.RunTerminal([]string{"y"}, "sync")

	if res.ExitCode != 0 {
		t.Fatalf("exit code = %d, transcript:\n%s", res.ExitCode, res.Stdout)
	}
	for _, secret := range []string{"old-token-value", "new-token-value"} {
		if strings.Contains(res.Stdout, secret) {
			t.Errorf("transcript shows %q:\n%s", secret, res.Stdout)
		}
	}
	if !strings.Contains(res.Stdout, `-  "type": "sse",`) {
		t.Errorf("transcript lacks the change:\n%s", res.Stdout)
	}
}

func TestMCP_AQuestionAboutOnlyASecretSaysSoInsteadOfShowingNothing(t *testing.T) {
	env := newEnv(t)
	profile := withMCP(t, env, sentryMCP, "SENTRY_TOKEN=s3cret-value\n")
	mcpFile := afterFirstSync(t, env, profile)
	writeClaudeJSON(t, env, mcpFile, strings.Replace(env.ReadFile(mcpFile), "Bearer s3cret-value", "Bearer pasted-by-hand", 1))

	res := env.RunTerminal([]string{"y"}, "sync")

	if res.ExitCode != 0 {
		t.Fatalf("exit code = %d, transcript:\n%s", res.ExitCode, res.Stdout)
	}
	if !strings.Contains(res.Stdout, "only secret values differ in mcpServers.sentry") {
		t.Errorf("transcript lacks the note:\n%s", res.Stdout)
	}
	if strings.Contains(res.Stdout, "pasted-by-hand") || strings.Contains(res.Stdout, "s3cret-value") {
		t.Errorf("transcript shows a secret:\n%s", res.Stdout)
	}
	if got := env.ReadFile(mcpFile); !strings.Contains(got, "Bearer s3cret-value") {
		t.Errorf(".claude.json = %q, want Baton's value back", got)
	}
}

func TestMCP_TheDefaultProfileKeepsItsMCPConfigurationInHomeNotInItsDirectory(t *testing.T) {
	env := newEnv(t)
	env.SourceFile("mcp.json", sentryMCP)
	env.CommitSource("add mcp")
	profile := env.MakeProfileDir(".claude")
	env.WriteConfig(`source = "` + env.SourceDir + `"

[profiles.claude]
agent = "claude"
dir = "` + profile + `"
`)
	writeSecrets(t, env, 0o600, "SENTRY_TOKEN=s3cret-value\n")

	res := env.Run("sync")

	if res.ExitCode != 0 {
		t.Fatalf("exit code = %d, stderr=%q", res.ExitCode, res.Stderr)
	}
	if got := env.ReadFile(".claude.json"); !strings.Contains(got, "Bearer s3cret-value") {
		t.Errorf("~/.claude.json = %q, want Baton's server in it", got)
	}
	if mode := env.Mode(".claude.json"); mode != 0o600 {
		t.Errorf("~/.claude.json mode = %v, want 0600", mode)
	}
	if env.Exists(filepath.Join(profile, ".claude.json")) {
		t.Error("sync wrote ~/.claude/.claude.json, which Claude does not read for the default Profile")
	}
	want := map[string]string{"../.claude.json mcpServers.sentry": "22ef0f5d3c356827ba02928bcf010dc6d6efe731db101ac4b2cba2c0711bd211"}
	if got := manifestKeys(t, env, "claude"); !reflect.DeepEqual(got, want) {
		t.Errorf("manifest keys = %v, want %v", got, want)
	}

	// The manifest must read back: a second sync changes nothing, and removing
	// the server from the source removes it from ~/.claude.json.
	modTime := env.ModTime(".claude.json")
	if res := env.Run("sync"); res.ExitCode != 0 {
		t.Fatalf("second sync: exit code = %d, stderr=%q", res.ExitCode, res.Stderr)
	}
	if !env.ModTime(".claude.json").Equal(modTime) {
		t.Error("the second sync rewrote ~/.claude.json")
	}
	env.SourceFile("mcp.json", `{"mcpServers": {}}`)
	if res := env.Run("sync"); res.ExitCode != 0 {
		t.Fatalf("removal: exit code = %d, stderr=%q", res.ExitCode, res.Stderr)
	}
	if got := env.ReadFile(".claude.json"); strings.Contains(got, "sentry") {
		t.Errorf("~/.claude.json = %q, want the server removed", got)
	}
}

func TestMCP_NeverWritesSecretsIntoAFileOthersCanRead(t *testing.T) {
	for _, mode := range []os.FileMode{0o664, 0o640, 0o604} {
		t.Run(mode.String(), func(t *testing.T) {
			env := newEnv(t)
			profile := withMCP(t, env, sentryMCP, "SENTRY_TOKEN=s3cret-value\n")
			env.SourceFile("skills/greet/SKILL.md", "Hello.\n")
			env.CommitSource("add skill")
			mcpFile := filepath.Join(profile, ".claude.json")
			env.WriteFile(mcpFile, existingClaudeJSON)
			if err := os.Chmod(mcpFile, mode); err != nil {
				t.Fatal(err)
			}

			res := env.Run("sync")

			if res.ExitCode == 0 {
				t.Fatalf("exit code = 0, want non-zero; stdout=%q", res.Stdout)
			}
			if !strings.Contains(res.Stderr, mcpFile) || !strings.Contains(res.Stderr, "chmod 600 "+mcpFile) {
				t.Errorf("stderr = %q, want it to name %s and suggest chmod 600", res.Stderr, mcpFile)
			}
			if strings.Contains(res.Stderr+res.Stdout, "s3cret-value") {
				t.Error("a secret value was printed")
			}
			if got := env.ReadFile(mcpFile); got != existingClaudeJSON {
				t.Errorf(".claude.json = %q, want it untouched", got)
			}
			if got := env.Mode(mcpFile); got != mode {
				t.Errorf(".claude.json mode = %v, want %v: Baton does not own the file, so it does not chmod it", got, mode)
			}
			if env.Exists(filepath.Join(profile, "skills")) || env.Exists(".baton") {
				t.Error("sync wrote something")
			}
		})
	}
}

func TestMCP_AFileOthersCanReadIsFineWhenNoServerHasASecret(t *testing.T) {
	env := newEnv(t)
	profile := withMCP(t, env, `{"mcpServers": {"docs": {"type": "http", "url": "https://docs.example.test/mcp"}}}`, "")
	mcpFile := filepath.Join(profile, ".claude.json")
	env.WriteFile(mcpFile, existingClaudeJSON) // mode 0644

	res := env.RunTerminal([]string{"y"}, "sync")

	if res.ExitCode != 0 {
		t.Fatalf("exit code = %d, transcript:\n%s", res.ExitCode, res.Stdout)
	}
	if got := env.ReadFile(mcpFile); !strings.Contains(got, `"docs"`) {
		t.Errorf(".claude.json = %q, want the server in it", got)
	}
	if mode := env.Mode(mcpFile); mode != 0o644 {
		t.Errorf(".claude.json mode = %v, want it kept", mode)
	}
}
