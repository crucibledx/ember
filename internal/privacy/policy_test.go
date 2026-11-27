package privacy

import (
	"reflect"
	"strings"
	"testing"

	"go.opentelemetry.io/collector/pdata/pcommon"
)

func newTestPolicy(t *testing.T, cfg Config) *Policy {
	t.Helper()
	if cfg.HumanIdentity == "" {
		cfg.HumanIdentity = HumanPlain
	}
	p, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestEveryEmberKeySurvives(t *testing.T) {
	attrs := pcommon.NewMap()
	for _, key := range strings.Fields(emberKeys) {
		attrs.PutStr(key, "value")
	}
	dropped := newTestPolicy(t, Config{}).Filter(attrs)
	if len(dropped) != 0 {
		t.Fatalf("dropped Ember keys: %v", dropped)
	}
	if attrs.Len() != len(strings.Fields(emberKeys)) {
		t.Fatalf("attributes remaining = %d", attrs.Len())
	}
}

func TestNativeAllowRequiresMatchedAdapter(t *testing.T) {
	attrs := pcommon.NewMap()
	attrs.PutStr("native.safe", "value")
	dropped := newTestPolicy(t, Config{}).Filter(attrs)
	if !reflect.DeepEqual(dropped, map[string]int{"native.safe": 1}) {
		t.Fatalf("unmatched drops = %v", dropped)
	}

	attrs.PutStr("native.safe", "value")
	attrs.PutStr("custom.safe", "value")
	p := newTestPolicy(t, Config{NativeAllow: []string{"native.safe"}, Allow: []string{"custom.safe"}})
	if dropped := p.Filter(attrs); len(dropped) != 0 || attrs.Len() != 2 {
		t.Fatalf("matched and custom attributes: drops=%v attrs=%v", dropped, attrs.AsRaw())
	}
}

func TestDenyWinsAndEmailIsAlwaysDenied(t *testing.T) {
	attrs := pcommon.NewMap()
	attrs.PutStr("custom.key", "value")
	attrs.PutStr("session.id", "value")
	attrs.PutStr("user.email", "private-value")
	p := newTestPolicy(t, Config{
		Allow:       []string{"custom.key", "user.email"},
		Deny:        []string{"custom.key", "session.id"},
		NativeAllow: []string{"user.email"},
	})
	want := map[string]int{"custom.key": 1, "session.id": 1, "user.email": 1}
	if got := p.Filter(attrs); !reflect.DeepEqual(got, want) || attrs.Len() != 0 {
		t.Fatalf("drops=%v attrs=%v", got, attrs.AsRaw())
	}
}

func TestContentKeysBodiesAndStatus(t *testing.T) {
	for _, content := range []bool{false, true} {
		t.Run(map[bool]string{false: "off", true: "on"}[content], func(t *testing.T) {
			p := newTestPolicy(t, Config{Content: content, ContentKeys: []string{"native.content"}})
			attrs := pcommon.NewMap()
			attrs.PutStr("native.content", "private-value")
			attrs.PutStr("event.name", "native.event")
			dropped := p.Filter(attrs)
			_, contentPresent := attrs.Get("native.content")
			if contentPresent != content || (dropped["native.content"] == 1) == content {
				t.Fatalf("content present=%v drops=%v", contentPresent, dropped)
			}

			body := pcommon.NewValueStr("private body")
			p.FilterLogBody(body, attrs)
			wantBody := ""
			if content {
				wantBody = "private body"
			} else {
				wantBody = "native.event"
			}
			if body.Str() != wantBody {
				t.Errorf("body = %q, want %q", body.Str(), wantBody)
			}
			message := p.FilterStatusMessage("private status")
			if content && message != "private status" || !content && message != "" {
				t.Errorf("status message = %q", message)
			}
		})
	}

	p := newTestPolicy(t, Config{})
	body := pcommon.NewValueStr("private body")
	p.FilterLogBody(body, pcommon.NewMap())
	if body.Str() != "" {
		t.Fatalf("body without event.name = %q", body.Str())
	}
}

func TestHumanIdentityHash(t *testing.T) {
	attrs := pcommon.NewMap()
	attrs.PutStr("ember.human.started_by", "person-1")
	attrs.PutStr("ember.human.active", "person-1,h1:already,person-2,")
	attrs.PutStr("actor.id", "person-2")
	attrs.PutStr("ember.human.empty", "")
	p := newTestPolicy(t, Config{
		HumanIdentity: HumanHash,
		HashKey:       "test hash key",
		OwnerKeys:     []string{"actor.id"},
		NativeAllow:   []string{"actor.id"},
		Allow:         []string{"ember.human.empty"},
	})
	if dropped := p.Filter(attrs); len(dropped) != 0 {
		t.Fatal(dropped)
	}
	want := map[string]any{
		"ember.human.started_by": "h1:1a8c616c975cd970",
		"ember.human.active":     "h1:1a8c616c975cd970,h1:already,h1:ec900ef32ea11d74,",
		"actor.id":               "h1:ec900ef32ea11d74",
		"ember.human.empty":      "",
	}
	if got := attrs.AsRaw(); !reflect.DeepEqual(got, want) {
		t.Fatalf("hashed attributes = %v, want %v", got, want)
	}
}

func TestHumanIdentityPlainAndOff(t *testing.T) {
	for _, identity := range []HumanIdentity{HumanPlain, HumanOff} {
		t.Run(string(identity), func(t *testing.T) {
			attrs := pcommon.NewMap()
			attrs.PutStr("ember.human.started_by", "person-1")
			attrs.PutStr("actor.id", "person-2")
			attrs.PutStr("session.id", "run-1")
			p := newTestPolicy(t, Config{
				HumanIdentity: identity,
				OwnerKeys:     []string{"actor.id"},
				NativeAllow:   []string{"actor.id"},
			})
			dropped := p.Filter(attrs)
			if identity == HumanPlain {
				if len(dropped) != 0 || attrs.Len() != 3 {
					t.Fatalf("plain: drops=%v attrs=%v", dropped, attrs.AsRaw())
				}
				return
			}
			want := map[string]int{"ember.human.started_by": 1, "actor.id": 1}
			if !reflect.DeepEqual(dropped, want) || attrs.Len() != 1 {
				t.Fatalf("off: drops=%v attrs=%v", dropped, attrs.AsRaw())
			}
		})
	}
}

func TestConfigValidation(t *testing.T) {
	if _, err := New(Config{HumanIdentity: "unknown"}); err == nil || !strings.Contains(err.Error(), "use hash, plain, or off") {
		t.Fatalf("unknown identity error = %v", err)
	}
	if _, err := New(Config{HumanIdentity: HumanHash}); err == nil || !strings.Contains(err.Error(), "nonempty hash key") {
		t.Fatalf("missing key error = %v", err)
	}
	if _, err := New(Config{}); err == nil {
		t.Fatal("default hash mode accepted an empty key")
	}
}

func TestDroppedCountsContainNoValues(t *testing.T) {
	attrs := pcommon.NewMap()
	attrs.PutStr("private.attribute", "planted-secret")
	attrs.PutStr("private.attribute.two", "planted-secret")
	p, err := New(Config{HashKey: "test hash key"})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]int{"private.attribute": 1, "private.attribute.two": 1}
	got := p.Filter(attrs)
	if !reflect.DeepEqual(got, want) || attrs.Len() != 0 {
		t.Fatalf("drops=%v attrs=%v", got, attrs.AsRaw())
	}
	for key, value := range got {
		if strings.Contains(key, "planted-secret") || value != 1 {
			t.Fatalf("drop report leaked a value: %v", got)
		}
	}
}
