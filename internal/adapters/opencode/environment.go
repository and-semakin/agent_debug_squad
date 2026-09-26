package opencode

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/url"
	"sort"
	"strings"

	"github.com/and-semakin/agent_debug_squad/internal/domain"
	"github.com/tailscale/hujson"
)

var baselineEnv = []string{"HOME", "PATH", "TMPDIR", "TMP", "TEMP", "XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_CACHE_HOME", "XDG_STATE_HOME"}

func processEnvironment(s domain.MachineBackendSettings, ambient []string, password string) ([]string, *strings.Replacer, error) {
	source := map[string]string{}
	for _, item := range ambient {
		if k, v, ok := strings.Cut(item, "="); ok {
			source[k] = v
		}
	}
	env := map[string]string{}
	for _, key := range append(append([]string{}, baselineEnv...), s.InheritEnv...) {
		if value, ok := source[key]; ok {
			env[key] = value
		}
	}
	secrets := []string{password, base64.StdEncoding.EncodeToString([]byte("opencode:" + password))}
	for _, key := range []string{"HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "http_proxy", "https_proxy", "all_proxy"} {
		secrets = append(secrets, proxySecrets(env[key])...)
	}
	if s.ProxyURL != "" {
		secrets = append(secrets, proxySecrets(s.ProxyURL)...)
		for _, key := range []string{"ALL_PROXY", "all_proxy", "http_proxy", "https_proxy"} {
			delete(env, key)
		}
		env["HTTP_PROXY"], env["HTTPS_PROXY"] = s.ProxyURL, s.ProxyURL
	}
	seen := map[string]bool{}
	var noProxy []string
	for _, list := range []string{env["NO_PROXY"], env["no_proxy"], s.NoProxy, "localhost,127.0.0.1,::1"} {
		for _, item := range strings.Split(list, ",") {
			item = strings.TrimSpace(item)
			if item != "" && !seen[item] {
				seen[item] = true
				noProxy = append(noProxy, item)
			}
		}
	}
	env["NO_PROXY"] = strings.Join(noProxy, ",")
	env["no_proxy"] = env["NO_PROXY"]
	cfg := map[string]json.RawMessage{}
	if raw := env["OPENCODE_CONFIG_CONTENT"]; raw != "" {
		standard, err := hujson.Standardize([]byte(raw))
		if err != nil || json.Unmarshal(standard, &cfg) != nil || cfg == nil {
			return nil, nil, errors.New("opencode inherited OPENCODE_CONFIG_CONTENT must be a JSONC object")
		}
	}
	snapshot := s.Snapshot != nil && *s.Snapshot
	cfg["snapshot"], _ = json.Marshal(snapshot)
	content, _ := json.Marshal(cfg)
	env["OPENCODE_CONFIG_CONTENT"] = string(content)
	env["OPENCODE_SERVER_USERNAME"] = "opencode"
	env["OPENCODE_SERVER_PASSWORD"] = password
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	result := make([]string, 0, len(keys))
	for _, k := range keys {
		result = append(result, k+"="+env[k])
	}
	return result, secretReplacer(secrets), nil
}

func proxySecrets(value string) []string {
	if value == "" {
		return nil
	}
	result := []string{value}
	if u, err := url.Parse(value); err == nil && u.User != nil {
		result = append(result, u.User.String(), u.User.Username(), url.QueryEscape(u.User.Username()), url.PathEscape(u.User.Username()))
		if p, ok := u.User.Password(); ok {
			result = append(result, p, url.QueryEscape(p), url.PathEscape(p), u.User.Username()+":"+p, base64.StdEncoding.EncodeToString([]byte(u.User.Username()+":"+p)))
		}
	}
	return result
}

func secretReplacer(secrets []string) *strings.Replacer {
	// Longest matches first to avoid leaving part of a URL behind.
	sort.Slice(secrets, func(i, j int) bool { return len(secrets[i]) > len(secrets[j]) })
	pairs := []string{}
	seen := map[string]bool{}
	for _, s := range secrets {
		if s != "" && !seen[s] {
			seen[s] = true
			pairs = append(pairs, s, "[REDACTED]")
			b, _ := json.Marshal(s)
			escaped := string(b[1 : len(b)-1])
			if escaped != s {
				pairs = append(pairs, escaped, "[REDACTED]")
			}
		}
	}
	return strings.NewReplacer(pairs...)
}
