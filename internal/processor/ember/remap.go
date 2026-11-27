package ember

import (
	"container/list"
	"sync"
	"time"

	"go.opentelemetry.io/collector/pdata/pcommon"

	"github.com/crucibledx/ember/internal/extension/registry"
)

type spanKey struct {
	trace pcommon.TraceID
	span  pcommon.SpanID
}

type remap struct {
	moved    bool
	run      registry.Key
	toolID   string
	newTrace pcommon.TraceID
	newSpan  pcommon.SpanID
	expires  time.Time
}

type remapItem struct {
	key   spanKey
	value remap
}

type remapCache struct {
	mu    sync.Mutex
	now   func() time.Time
	ttl   time.Duration
	limit int
	items map[spanKey]*list.Element
	lru   *list.List
}

func newRemapCache(limit int, ttl time.Duration) *remapCache {
	return &remapCache{now: time.Now, ttl: ttl, limit: limit, items: map[spanKey]*list.Element{}, lru: list.New()}
}

func (c *remapCache) get(key spanKey) (remap, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	element := c.items[key]
	if element == nil {
		return remap{}, false
	}
	item := element.Value.(remapItem)
	if !c.now().Before(item.value.expires) {
		c.remove(element)
		return remap{}, false
	}
	c.lru.MoveToFront(element)
	return item.value, true
}

func (c *remapCache) put(key spanKey, value remap) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.limit == 0 {
		return
	}
	value.expires = c.now().Add(c.ttl)
	if element := c.items[key]; element != nil {
		element.Value = remapItem{key: key, value: value}
		c.lru.MoveToFront(element)
		return
	}
	element := c.lru.PushFront(remapItem{key: key, value: value})
	c.items[key] = element
	for c.lru.Len() > c.limit {
		c.remove(c.lru.Back())
	}
}

func (c *remapCache) remove(element *list.Element) {
	item := element.Value.(remapItem)
	delete(c.items, item.key)
	c.lru.Remove(element)
}
