package sync

import (
	"container/heap"
	"time"
)

type (
	Key       = string
	QueueFunc func(Key)
)

type item struct {
	key          string
	creationTime time.Time
	index        int
}

type priorityQueue struct {
	items     []*item
	itemByKey map[string]*item
}

func (pq *priorityQueue) pop() *item {
	return heap.Pop(pq).(*item)
}

func (pq *priorityQueue) peek() *item {
	return pq.items[0]
}

func (pq *priorityQueue) add(key Key, creationTime time.Time) {
	heap.Push(pq, &item{key: key, creationTime: creationTime})
}

func (pq *priorityQueue) remove(key Key) {
	if item, ok := pq.itemByKey[key]; ok {
		heap.Remove(pq, item.index)
		delete(pq.itemByKey, key)
	}
}

func (pq priorityQueue) Len() int { return len(pq.items) }

func (pq priorityQueue) Less(i, j int) bool {
	return pq.items[i].creationTime.Before(pq.items[j].creationTime)
}

func (pq priorityQueue) Swap(i, j int) {
	pq.items[i], pq.items[j] = pq.items[j], pq.items[i]
	pq.items[i].index = i
	pq.items[j].index = j
}

func (pq *priorityQueue) Push(x interface{}) {
	n := len(pq.items)
	item, _ := x.(*item)
	item.index = n
	pq.items = append(pq.items, item)
	pq.itemByKey[item.key] = item
}

func (pq *priorityQueue) Pop() interface{} {
	old := pq.items
	n := len(old)
	item := old[n-1]
	item.index = -1
	pq.items = old[0 : n-1]
	delete(pq.itemByKey, item.key)
	return item
}