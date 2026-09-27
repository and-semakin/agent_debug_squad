package modelprobe

import (
	"encoding/base64"
	"encoding/json"
	"github.com/and-semakin/agent_debug_squad/internal/domain"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

var credentialURL = regexp.MustCompile(`https?://[^\s"<>]+`)

func SensitiveKey(k string) bool {
	k = strings.ToLower(strings.ReplaceAll(k, "_", ""))
	return strings.Contains(k, "secret") || strings.Contains(k, "token") || strings.Contains(k, "password") || strings.Contains(k, "apikey") || k == "authorization" || strings.Contains(k, "proxy") && k != "noproxy"
}

// Unlike environment names, arbitrary catalog map keys may contain words such
// as "token". Only actual credential field names taint their values.
func credentialField(k string) bool {
	switch strings.ToLower(strings.NewReplacer("_", "", "-", "").Replace(k)) {
	case "apikey", "token", "accesstoken", "refreshtoken", "idtoken", "secret", "clientsecret", "password", "authorization", "proxy", "proxyurl":
		return true
	}
	return false
}
func Secrets(v any) []string {
	var out []string
	var walk func(any, bool)
	walk = func(v any, secret bool) {
		switch x := v.(type) {
		case string:
			if secret && x != "" {
				out = append(out, x)
			}
		case map[string]any:
			for k, v := range x {
				walk(v, secret || credentialField(k))
			}
		case []any:
			for _, v := range x {
				walk(v, secret)
			}
		}
	}
	walk(v, false)
	return out
}
func EnvSecrets(env []string) []string {
	var out []string
	for _, e := range env {
		k, v, ok := strings.Cut(e, "=")
		if ok && SensitiveKey(k) && v != "" {
			out = append(out, v)
		}
	}
	return out
}
func Cleaner(secrets []string) func(string) string {
	values := map[string]bool{}
	for _, s := range secrets {
		if s == "" {
			continue
		}
		values[s] = true
		values[url.QueryEscape(s)] = true
		values[url.PathEscape(s)] = true
		values[base64.StdEncoding.EncodeToString([]byte(s))] = true
		if u, e := url.Parse(s); e == nil && u.User != nil {
			values[u.User.Username()] = true
			if p, ok := u.User.Password(); ok {
				values[p] = true
				values[url.QueryEscape(p)] = true
			}
		}
	}
	keys := []string{}
	for s := range values {
		if s != "" {
			keys = append(keys, s)
		}
	}
	sort.Slice(keys, func(i, j int) bool {
		if len(keys[i]) == len(keys[j]) {
			return keys[i] < keys[j]
		}
		return len(keys[i]) > len(keys[j])
	})
	pairs := []string{}
	for _, s := range keys {
		pairs = append(pairs, s, "[redacted]")
	}
	replace := strings.NewReplacer(pairs...)
	return func(s string) string {
		s = replace.Replace(s)
		s = credentialURL.ReplaceAllStringFunc(s, func(v string) string {
			u, e := url.Parse(v)
			if e != nil || u.User != nil || u.RawQuery != "" {
				return "[redacted-url]"
			}
			return v
		})
		return s
	}
}
func safeID(s string) bool {
	return s != "" && len(s) <= 4096 && utf8.ValidString(s) && !strings.ContainsFunc(s, func(r rune) bool { return unicode.IsControl(r) || unicode.Is(unicode.Cf, r) })
}
func Sanitize(r *domain.ModelDiscoveryResult, secrets []string) {
	clean := Cleaner(secrets)
	models := r.Models
	r.Models = []domain.CatalogModel{}
	for _, m := range models {
		bad := !safeID(m.ModelID)
		for _, s := range []string{m.ModelID, m.ProviderID, m.Alias, m.CatalogID} {
			if s != "" && (!safeID(s) || clean(s) != s) {
				bad = true
			}
		}
		for _, s := range m.Selection.Options {
			if !safeID(s) || clean(s) != s {
				bad = true
			}
		}
		if bad {
			Fail(r, "error", "unsafe_model_metadata")
			continue
		}
		b, _ := json.Marshal(m)
		var obj any
		_ = json.Unmarshal(b, &obj)
		var scrub func(any) any
		scrub = func(v any) any {
			switch x := v.(type) {
			case string:
				x = clean(x)
				if len(x) > 4096 {
					x = x[:4096]
					Fail(r, "partial", "metadata_limit")
				}
				return x
			case []any:
				if len(x) > 256 {
					x = x[:256]
					Fail(r, "partial", "metadata_limit")
				}
				for i := range x {
					x[i] = scrub(x[i])
				}
				return x
			case map[string]any:
				for k, v := range x {
					x[k] = scrub(v)
				}
				return x
			}
			return v
		}
		b, _ = json.Marshal(scrub(obj))
		_ = json.Unmarshal(b, &m)
		r.Models = append(r.Models, m)
	}
}
