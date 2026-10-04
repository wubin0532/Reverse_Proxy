package webproxy

import (
	"net/url"
	"strings"

	"andey-proxy/internal/config"
)

// Reverse the request path mapping only for a redirect inside this backend's
// mounted subtree. External paths, query strings and fragments stay intact.
func rewriteLocationPath(location, target *url.URL, rule config.SubRule) {
	if !rule.StripPrefix {
		return
	}
	base := strings.TrimSuffix(target.Path, "/")
	if base == "" {
		base = "/"
	}
	if !pathMatch(base, location.Path) || ambiguousPath(location.Path) {
		return
	}
	stripped := stripFrontendURL(location, base)
	prefix := &url.URL{Path: normalizedFrontendPrefix(rule.FrontendPath)}
	location.Path, location.RawPath = joinProxyURLPath(prefix, stripped)
}
