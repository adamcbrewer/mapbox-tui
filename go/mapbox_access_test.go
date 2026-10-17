package main

import "testing"

func TestMapboxProductAccess(t *testing.T) {
	for _, test := range []struct {
		name                     string
		offline, accepted, token bool
		want                     mapboxAccess
	}{
		{name: "online", accepted: true, token: true, want: mapboxAllowed},
		{name: "consent required", token: true, want: mapboxNeedsConsent},
		{name: "offline overrides consent", offline: true, accepted: true, token: true, want: mapboxOffline},
		{name: "offline before consent", offline: true, token: true, want: mapboxOffline},
		{name: "offline without token", offline: true, want: mapboxOffline},
		{name: "no token", accepted: true, want: mapboxNoCredential},
		{name: "no token or consent", want: mapboxNoCredential},
	} {
		t.Run(test.name, func(t *testing.T) {
			access := decideMapboxAccess(test.offline, test.accepted, test.token)
			if access != test.want {
				t.Fatal("unexpected product access", access, test.want)
			}
			credential := renderingCredential{source: "environment", guidance: "No configured token"}
			if test.token {
				credential.token = "pk.fixture"
			}
			effective := access.effectiveCredential(credential)
			if (effective.token != "") != (test.want == mapboxAllowed) || effective.source != credential.source {
				t.Fatal("policy lost provenance or enabled a blocked credential", effective.source)
			}
			if access == mapboxNoCredential && effective.guidance != credential.guidance {
				t.Fatal("policy replaced credential-discovery guidance")
			}
		})
	}
}
