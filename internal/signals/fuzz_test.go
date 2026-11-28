package signals

import "testing"

func FuzzSignalBody(f *testing.F) {
	f.Add([]byte(`{"apiVersion":"vigil.signal/v0","source":"d","kind":"k","severity":"info"}`))
	f.Fuzz(func(t *testing.T, body []byte) {
		if len(body) > MaxBody {
			return
		}
		_, _ = DecodePost(body)
	})
}