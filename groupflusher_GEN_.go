package store

import (
	"sync"
	"sync/atomic"
	"time"
)

type groupFlusherState struct {
	interval         int
	flusherThreshold int32
	notifyChanLock   sync.Mutex
	notifyChan       chan *bgNotification
}

func (store *DefaultGroupStore) flusherConfig(cfg *GroupStoreConfig) {
	store.flusherState.interval = 60
	store.flusherState.flusherThreshold = cfg.FlusherThreshold
}

func (store *DefaultGroupStore) EnableFlusher() {
	store.flusherState.notifyChanLock.Lock()
	if store.flusherState.notifyChan == nil {
		store.flusherState.notifyChan = make(chan *bgNotification, 1)
		go store.flusherLauncher(store.flusherState.notifyChan)
	}
	store.flusherState.notifyChanLock.Unlock()
}

func (store *DefaultGroupStore) DisableFlusher() {
	store.flusherState.notifyChanLock.Lock()
	if store.flusherState.notifyChan != nil {
		c := make(chan struct{}, 1)
		store.flusherState.notifyChan <- &bgNotification{
			action:   _BG_DISABLE,
			doneChan: c,
		}
		<-c
		store.flusherState.notifyChan = nil
	}
	store.flusherState.notifyChanLock.Unlock()
}

func (store *DefaultGroupStore) flusherLauncher(notifyChan chan *bgNotification) {
	interval := float64(store.flusherState.interval) * float64(time.Second)
	store.randMutex.Lock()
	nextRun := time.Now().Add(time.Duration(interval + interval*store.rand.NormFloat64()*0.1))
	store.randMutex.Unlock()
	running := true
	for running {
		var notification *bgNotification
		sleep := nextRun.Sub(time.Now())
		if sleep > 0 {
			select {
			case notification = <-notifyChan:
			case <-time.After(sleep):
			}
		} else {
			select {
			case notification = <-notifyChan:
			default:
			}
		}
		store.randMutex.Lock()
		nextRun = time.Now().Add(time.Duration(interval + interval*store.rand.NormFloat64()*0.1))
		store.randMutex.Unlock()
		if notification != nil {
			if notification.action == _BG_DISABLE {
				running = false
			} else {
				store.logCritical("flusher: invalid action requested: %d", notification.action)
			}
			notification.doneChan <- struct{}{}
			continue
		}
		m := atomic.LoadInt32(&store.modifications)
		atomic.AddInt32(&store.modifications, -m)
		if store.logDebug != nil && m > 0 && m < store.flusherState.flusherThreshold {
			store.logDebug("flusher: %d modifications under %d threshold; flushing.", m, store.flusherState.flusherThreshold)
			store.Flush()
		}
	}
}
