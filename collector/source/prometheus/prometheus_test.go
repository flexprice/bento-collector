package prometheus

import (
	"testing"

	"github.com/flexprice/bento-collector/collector/spec"
)

func TestValidateConnection(t *testing.T) {
	for auth, ok := range map[string]bool{"": true, "none": true, "basic": true, "bearer": true, "api_key": false} {
		err := Provider{}.ValidateConnection(spec.Connection{ID: "p", Type: Type, URL: "http://x", AuthType: auth})
		if (err == nil) != ok {
			t.Errorf("auth %q: err=%v", auth, err)
		}
	}
}
