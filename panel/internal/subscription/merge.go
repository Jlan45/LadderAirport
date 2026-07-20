package subscription

import (
	"fmt"
	"strings"
)

// MergeEndpoints concatenates local then external endpoints and uniquifies names.
func MergeEndpoints(local, external []ProxyEndpoint) []ProxyEndpoint {
	n := len(local) + len(external)
	if n == 0 {
		return []ProxyEndpoint{}
	}
	out := make([]ProxyEndpoint, 0, n)
	out = append(out, local...)
	out = append(out, external...)
	return uniquifyNames(out)
}

// prefixExternalName builds "{source}-{proxy}" and sanitizes.
func prefixExternalName(sourceName, proxyName string) string {
	return sanitizeName(fmt.Sprintf("%s-%s", sourceName, proxyName))
}

func uniquifyNames(eps []ProxyEndpoint) []ProxyEndpoint {
	seen := map[string]int{}
	for i := range eps {
		base := eps[i].Name
		if base == "" {
			base = "proxy"
			eps[i].Name = base
		}
		if n, ok := seen[strings.ToLower(eps[i].Name)]; ok {
			// append -2, -3, ...
			for {
				n++
				candidate := fmt.Sprintf("%s-%d", base, n)
				if _, exists := seen[strings.ToLower(candidate)]; !exists {
					eps[i].Name = candidate
					seen[strings.ToLower(candidate)] = 1
					seen[strings.ToLower(base)] = n
					break
				}
			}
		} else {
			seen[strings.ToLower(eps[i].Name)] = 1
		}
	}
	return eps
}
