package types

import (
	"container/heap"
	"math"
	"testing"
)

func TestCheckBlockHeightHash(t *testing.T) {
	d := &cachedBlockData{&BlockDataBasic{Height: 1001}, nil}
	height, hash, mismatch := checkBlockHeightHash(d)
	if mismatch {
		t.Errorf("checkBlockHeightHash: %v", mismatch)
	}
	t.Log(height, hash)
}

// TODO: Make a proper test rather than a playground

func TestBlockPriorityQueue(t *testing.T) {
	pq := NewBlockPriorityQueue(5)
	//pq.SetLessFn(LessByAccessCountThenHeight)
	//pq.SetLessFn(LessByAccessCount)
	//pq.SetLessFn(LessByAccessTime)
	//pq.SetLessFn(LessByHeight)
	pq.SetLessFn(MakeLessByAccessTimeThenCount(SecondsPerDay))

	cachedBlocks := []*CachedBlock{
		newCachedBlock(&BlockDataBasic{
			Height: 123,
		}, nil),
		newCachedBlock(&BlockDataBasic{
			Height: 1000,
		}, nil),
		newCachedBlock(&BlockDataBasic{
			Height: 1,
		}, nil),
		newCachedBlock(&BlockDataBasic{
			Height: 400,
		}, nil),
	}

	// reheap, which resets all access counts and times
	pq.ResetHeap(cachedBlocks)

	// forge the access counts
	cachedBlocks[0].accesses = 1
	cachedBlocks[1].accesses = 2
	cachedBlocks[2].accesses = 10
	cachedBlocks[3].accesses = 4
	heap.Init(pq)

	t.Log(pq.capacity, pq.Len(), pq.minHeight, pq.maxHeight)

	pq.Insert(&BlockDataBasic{Height: 1001}, nil)
	pq.Insert(&BlockDataBasic{Height: 0}, nil)
	pq.Insert(&BlockDataBasic{Height: 6}, nil)

	for pq.Len() > 0 {
		cachedBlock := heap.Pop(pq).(*CachedBlock)
		t.Logf("%8d\t%4d\t%d\t%4d\n", cachedBlock.summary.Height, cachedBlock.accesses, cachedBlock.accessTime, pq.Len())
	}

	blockData := &cachedBlockData{&BlockDataBasic{Height: 1}, nil}
	heap.Push(pq, blockData)
}

func TestBlockPriorityQueueRemoveBlock(t *testing.T) {
	pq := NewBlockPriorityQueue(5)
	pq.SetLessFn(LessByHeight) // the lowest height is at the root

	heights := []uint32{50, 10, 30, 20, 40}
	for _, h := range heights {
		if added, _, _ := pq.Insert(&BlockDataBasic{Height: h}, nil); !added {
			t.Fatalf("block at height %d not added", h)
		}
	}

	checkMinMax := func(min, max int64) {
		t.Helper()
		if pq.minHeight != min || pq.maxHeight != max {
			t.Fatalf("min/max heights = %d/%d, want %d/%d",
				pq.minHeight, pq.maxHeight, min, max)
		}
	}
	checkMinMax(10, 50)

	// Remove the root, which is also the minimum height.
	root := pq.bh[0]
	if root.height != 10 {
		t.Fatalf("root height = %d, want 10", root.height)
	}
	pq.RemoveBlock(root)
	if pq.Len() != 4 {
		t.Fatalf("Len = %d, want 4", pq.Len())
	}
	if root.heapIdx != -1 {
		t.Errorf("removed block heap index = %d, want -1", root.heapIdx)
	}
	checkMinMax(20, 50)

	// Remove the maximum height block.
	var maxBlock *CachedBlock
	for _, b := range pq.bh {
		if b.height == 50 {
			maxBlock = b
		}
	}
	pq.RemoveBlock(maxBlock)
	if pq.Len() != 3 {
		t.Fatalf("Len = %d, want 3", pq.Len())
	}
	checkMinMax(20, 40)

	// Removing a block that is not in the queue is a no-op.
	pq.RemoveBlock(newCachedBlock(&BlockDataBasic{Height: 30, Hash: "x"}, nil))
	if pq.Len() != 3 {
		t.Fatalf("Len = %d, want 3", pq.Len())
	}

	for pq.Len() > 0 {
		pq.RemoveBlock(pq.bh[0])
	}
	checkMinMax(math.MaxUint32, -1)
}
