package update

import (
	"regexp"
	"strings"
)

// Release tags may start with v, but otherwise must follow Semantic Versioning
// 2.0.0. Numeric components stay strings so arbitrarily large valid versions do
// not overflow an integer and accidentally permit a downgrade.
var semanticVersion = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:-([0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*))?(?:\+([0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*))?$`)

type releaseVersion struct {
	core [3]string
	pre  []string
}

// Available reports whether tag is a strictly newer semantic version than the
// running build. Build metadata does not affect precedence. Unknown versions,
// including local "dev" builds, never authorize an automatic replacement.
// ApplyTo remains available for an explicitly chosen release.
func Available(tag string) bool {
	installed, ok := parseVersion(Version)
	if !ok {
		return false
	}
	candidate, ok := parseVersion(tag)
	return ok && candidate.compare(installed) > 0
}

func parseVersion(value string) (releaseVersion, bool) {
	parts := semanticVersion.FindStringSubmatch(strings.TrimPrefix(value, "v"))
	if parts == nil {
		return releaseVersion{}, false
	}
	version := releaseVersion{core: [3]string{parts[1], parts[2], parts[3]}}
	if parts[4] != "" {
		version.pre = strings.Split(parts[4], ".")
		for _, identifier := range version.pre {
			if numeric(identifier) && len(identifier) > 1 && identifier[0] == '0' {
				return releaseVersion{}, false
			}
		}
	}
	return version, true
}

func (version releaseVersion) compare(other releaseVersion) int {
	for index, component := range version.core {
		if order := compareNumeric(component, other.core[index]); order != 0 {
			return order
		}
	}
	if len(version.pre) == 0 && len(other.pre) == 0 {
		return 0
	}
	if len(version.pre) == 0 {
		return 1
	}
	if len(other.pre) == 0 {
		return -1
	}
	for index := 0; index < len(version.pre) && index < len(other.pre); index++ {
		left, right := version.pre[index], other.pre[index]
		leftNumeric, rightNumeric := numeric(left), numeric(right)
		var order int
		switch {
		case leftNumeric && rightNumeric:
			order = compareNumeric(left, right)
		case leftNumeric:
			order = -1
		case rightNumeric:
			order = 1
		default:
			order = strings.Compare(left, right)
		}
		if order != 0 {
			return order
		}
	}
	switch {
	case len(version.pre) < len(other.pre):
		return -1
	case len(version.pre) > len(other.pre):
		return 1
	default:
		return 0
	}
}

func numeric(value string) bool {
	for index := range value {
		if value[index] < '0' || value[index] > '9' {
			return false
		}
	}
	return value != ""
}

func compareNumeric(left, right string) int {
	switch {
	case len(left) < len(right):
		return -1
	case len(left) > len(right):
		return 1
	default:
		return strings.Compare(left, right)
	}
}
