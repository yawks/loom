package slack

import (
	"Loom/pkg/core"
	"testing"
)

func TestSlackConnectionCredentialsSharesOAuthTokenBetweenModes(t *testing.T) {
	config := core.ProviderConfig{
		"slack_mode": "official",
		"token":      "xoxp-shared",
		"app_token":  "xapp-socket",
	}
	mode, token, appToken, err := slackConnectionCredentials(config)
	if err != nil {
		t.Fatal(err)
	}
	if mode != slackModeOfficial || token != "xoxp-shared" || appToken != "xapp-socket" {
		t.Fatalf("unexpected official credentials: mode=%q token=%q appToken=%q", mode, token, appToken)
	}
	config["slack_mode"] = "compatible"
	mode, token, appToken, err = slackConnectionCredentials(config)
	if err != nil {
		t.Fatal(err)
	}
	if mode != slackModeCompatible || token != "xoxp-shared" || appToken != "" {
		t.Fatalf("unexpected compatible credentials: mode=%q token=%q appToken=%q", mode, token, appToken)
	}
}

func TestSlackConnectionCredentialsValidatesOfficialTokens(t *testing.T) {
	for _, config := range []core.ProviderConfig{
		{"slack_mode": "official", "token": "", "app_token": "xapp-valid"},
		{"slack_mode": "official", "token": "xoxp-valid", "app_token": ""},
	} {
		if _, _, _, err := slackConnectionCredentials(config); err == nil {
			t.Fatalf("expected invalid official credentials to fail: %#v", config)
		}
	}
}

func TestSlackConnectionCredentialsDefaultsToCompatible(t *testing.T) {
	mode, token, _, err := slackConnectionCredentials(core.ProviderConfig{"token": "xoxp-existing"})
	if err != nil {
		t.Fatal(err)
	}
	if mode != slackModeCompatible || token != "xoxp-existing" {
		t.Fatalf("legacy configuration was not preserved: mode=%q token=%q", mode, token)
	}
}

func TestSlackConnectionCredentialsCanSelectBrowserWithoutDeletingStoredToken(t *testing.T) {
	config := core.ProviderConfig{
		"slack_mode":           "compatible",
		"compatible_auth_mode": "browser",
		"token":                "xoxp-kept-for-later",
		"app_token":            "xapp-official",
	}
	mode, token, appToken, err := slackConnectionCredentials(config)
	if err != nil {
		t.Fatal(err)
	}
	if mode != slackModeCompatible || token != "" || appToken != "" {
		t.Fatalf("browser mode selected unexpected credentials: mode=%q token=%q appToken=%q", mode, token, appToken)
	}
	if config["token"] != "xoxp-kept-for-later" || config["app_token"] != "xapp-official" {
		t.Fatalf("selecting browser authentication mutated stored credentials: %#v", config)
	}
}

func TestSlackConnectionCredentialsMigratesTemporaryOfficialTokenKey(t *testing.T) {
	config := core.ProviderConfig{
		"slack_mode":          "official",
		"official_user_token": "xoxp-from-temporary-key",
		"app_token":           "xapp-socket",
	}
	_, token, _, err := slackConnectionCredentials(config)
	if err != nil {
		t.Fatal(err)
	}
	if token != "xoxp-from-temporary-key" || config["token"] != token {
		t.Fatalf("temporary official token was not migrated: %#v", config)
	}
}
