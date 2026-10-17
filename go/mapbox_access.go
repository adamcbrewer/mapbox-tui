package main

// Product access is independent of saved-account statistics and of individual
// endpoint permissions. A Streets probe does not establish Directions access.
type mapboxAccess uint8

const (
	mapboxAllowed mapboxAccess = iota
	mapboxOffline
	mapboxNoCredential
	mapboxNeedsConsent
)

func decideMapboxAccess(offline, accepted, hasCredential bool) mapboxAccess {
	switch {
	case offline:
		return mapboxOffline
	case !hasCredential:
		return mapboxNoCredential
	case !accepted:
		return mapboxNeedsConsent
	default:
		return mapboxAllowed
	}
}

func (s appSettings) productAccess(token string) mapboxAccess {
	return decideMapboxAccess(s.Offline, s.billingAccepted(), token != "")
}

func (access mapboxAccess) guidance() string {
	switch access {
	case mapboxOffline:
		return "Mapbox offline mode is enabled; go online in Mapbox settings or run 'mapbox-tui auth online'"
	case mapboxNeedsConsent:
		return "Mapbox billing risks must be acknowledged; open Mapbox settings or run 'mapbox-tui auth online'. " +
			"For unattended use, review " + billingDocsURL + " then set MAPBOX_TUI_ACCEPT_BILLING=1"
	default:
		return ""
	}
}

func (access mapboxAccess) effectiveCredential(credential renderingCredential) renderingCredential {
	if access != mapboxAllowed {
		credential.token = ""
		if guidance := access.guidance(); guidance != "" {
			credential.guidance = guidance
		}
	}
	return credential
}
