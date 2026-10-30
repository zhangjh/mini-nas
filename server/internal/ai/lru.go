package ai

import "container/list"

// lruCache 是固定容量的 LRU。
//
// 只用于文本查询向量：检索页常见「改一个字再搜」「翻页重复请求」，
// 命中率很高，而重复编码一次要 20~50 ms。
//
// 存入的切片在生命周期内不可修改——调用方拿到的是同一份底层数组，
// 只读使用（检索只做点积）。
type lruCache struct {
	capacity int
	ll       *list.List
	items    map[string]*list.Element
}

type lruEntry struct {
	key string
	val []float32
}

func newLRU(capacity int) *lruCache {
	if capacity <= 0 {
		capacity = 1
	}
	return &lruCache{
		capacity: capacity,
		ll:       list.New(),
		items:    make(map[string]*list.Element, capacity),
	}
}

// Get 命中时把条目移到队首。
func (c *lruCache) Get(key string) ([]float32, bool) {
	el, ok := c.items[key]
	if !ok {
		return nil, false
	}
	c.ll.MoveToFront(el)
	return el.Value.(*lruEntry).val, true
}

// Put 写入并淘汰最久未使用的条目。
func (c *lruCache) Put(key string, val []float32) {
	if el, ok := c.items[key]; ok {
		el.Value.(*lruEntry).val = val
		c.ll.MoveToFront(el)
		return
	}
	el := c.ll.PushFront(&lruEntry{key: key, val: val})
	c.items[key] = el
	for c.ll.Len() > c.capacity {
		last := c.ll.Back()
		if last == nil {
			break
		}
		c.ll.Remove(last)
		delete(c.items, last.Value.(*lruEntry).key)
	}
}

// Len 返回当前条目数。
func (c *lruCache) Len() int { return c.ll.Len() }
