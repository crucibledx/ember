// Package privacy applies Ember's export privacy policy without performing I/O.
package privacy

import (
	"fmt"
	"strings"

	"go.opentelemetry.io/collector/pdata/pcommon"
)

// HumanIdentity controls how human identifiers are exported.
type HumanIdentity string

const (
	HumanHash  HumanIdentity = "hash"
	HumanPlain HumanIdentity = "plain"
	HumanOff   HumanIdentity = "off"
)

// Config supplies adapter-specific rules after the processor has matched an adapter.
type Config struct {
	Content       bool
	HumanIdentity HumanIdentity
	HashKey       string
	Allow         []string
	Deny          []string
	NativeAllow   []string
	ContentKeys   []string
	OwnerKeys     []string
}

// Policy is immutable and can be applied to every attribute location in a batch.
type Policy struct {
	allowed   map[string]struct{}
	content   bool
	human     HumanIdentity
	hashKey   []byte
	ownerKeys map[string]struct{}
}

// New validates config and builds one policy for a matched adapter, or for
// unmatched data when NativeAllow, ContentKeys, and OwnerKeys are empty.
func New(cfg Config) (*Policy, error) {
	identity := cfg.HumanIdentity
	if identity == "" {
		identity = HumanHash
	}
	if identity != HumanHash && identity != HumanPlain && identity != HumanOff {
		return nil, fmt.Errorf("privacy.human_identity %q is unknown; use hash, plain, or off", identity)
	}
	if identity == HumanHash && cfg.HashKey == "" {
		return nil, fmt.Errorf("privacy.human_identity hash requires a nonempty hash key")
	}

	p := &Policy{
		allowed:   keySet(strings.Fields(emberKeys)),
		content:   cfg.Content,
		human:     identity,
		hashKey:   []byte(cfg.HashKey),
		ownerKeys: keySet(cfg.OwnerKeys),
	}
	p.addAllowed(cfg.NativeAllow)
	p.addAllowed(cfg.Allow)
	if cfg.Content {
		p.addAllowed(cfg.ContentKeys)
	}
	for _, key := range cfg.Deny {
		delete(p.allowed, key)
	}
	delete(p.allowed, "user.email")
	return p, nil
}

func (p *Policy) addAllowed(keys []string) {
	for _, key := range keys {
		p.allowed[key] = struct{}{}
	}
}

// Filter removes disallowed attributes, applies human identity rules, and
// returns only dropped key names and counts. It never returns attribute values.
func (p *Policy) Filter(attrs pcommon.Map) map[string]int {
	dropped := map[string]int{}
	attrs.RemoveIf(func(key string, value pcommon.Value) bool {
		if _, ok := p.allowed[key]; !ok || p.human == HumanOff && p.isHuman(key) {
			dropped[key]++
			return true
		}
		if p.human == HumanHash && p.isHuman(key) && value.Type() == pcommon.ValueTypeStr {
			value.SetStr(hashHumanList(p.hashKey, value.Str()))
		}
		return false
	})
	return dropped
}

func (p *Policy) isHuman(key string) bool {
	if strings.HasPrefix(key, "ember.human.") {
		return true
	}
	_, ok := p.ownerKeys[key]
	return ok
}

// FilterLogBody keeps a body only when content is enabled. Otherwise it uses
// the record's event.name, or an empty string when that attribute is absent.
func (p *Policy) FilterLogBody(body pcommon.Value, attrs pcommon.Map) {
	if p.content {
		return
	}
	body.SetStr("")
	if event, ok := attrs.Get("event.name"); ok && event.Type() == pcommon.ValueTypeStr {
		body.SetStr(event.Str())
	}
}

// FilterStatusMessage preserves status codes by transforming only the message.
func (p *Policy) FilterStatusMessage(message string) string {
	if p.content {
		return message
	}
	return ""
}

func keySet(keys []string) map[string]struct{} {
	set := make(map[string]struct{}, len(keys))
	for _, key := range keys {
		set[key] = struct{}{}
	}
	return set
}

const emberKeys = `
service.name service.version service.instance.id host.name os.type host.arch ember.hash_key.id
session.id gen_ai.operation.name gen_ai.conversation.id gen_ai.agent.name gen_ai.request.model
ember.harness ember.agent.kind ember.workstream.id ember.work.id ember.parent.session.id
ember.parent.tool_call.id ember.human.started_by ember.human.active ember.session.segment
ember.agent.llm_turns ember.agent.tool_calls gen_ai.tool.name gen_ai.tool.call.id error.type
ember.tool.incomplete ember.mcp.server ember.mcp.tool ember.spawned.session_ids ember.link.type
event.name ember.agent.status ember.tool.duration_ms ember.llm.turn ember.root.session.id
ember.agent.children gen_ai.token.type ember.original_trace_id`
