package store

import (
	"errors"
	"io"
	"os"
	"path"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type groupAuditState struct {
	interval     int
	ageThreshold int64

	notifyChanLock sync.Mutex
	notifyChan     chan *bgNotification
}

func (store *DefaultGroupStore) auditConfig(cfg *GroupStoreConfig) {
	store.auditState.interval = cfg.AuditInterval
	store.auditState.ageThreshold = int64(cfg.AuditAgeThreshold) * int64(time.Second)
}

// AuditPass will immediately execute a pass at full speed to check the on-disk
// data for errors rather than waiting for the next interval to run the
// standard slow-audit pass. If a pass is currently executing, it will be
// stopped and restarted so that a call to this function ensures one complete
// pass occurs.
func (store *DefaultGroupStore) AuditPass() {
	store.auditState.notifyChanLock.Lock()
	if store.auditState.notifyChan == nil {
		store.auditPass(true, make(chan *bgNotification))
	} else {
		c := make(chan struct{}, 1)
		store.auditState.notifyChan <- &bgNotification{
			action:   _BG_PASS,
			doneChan: c,
		}
		<-c
	}
	store.auditState.notifyChanLock.Unlock()
}

// EnableAudit will resume audit passes. An audit pass checks on-disk data for
// errors.
func (store *DefaultGroupStore) EnableAudit() {
	store.auditState.notifyChanLock.Lock()
	if store.auditState.notifyChan == nil {
		store.auditState.notifyChan = make(chan *bgNotification, 1)
		go store.auditLauncher(store.auditState.notifyChan)
	}
	store.auditState.notifyChanLock.Unlock()
}

// DisableAudit will stop any audit passes until EnableAudit is called. An
// audit pass checks on-disk data for errors.
func (store *DefaultGroupStore) DisableAudit() {
	store.auditState.notifyChanLock.Lock()
	if store.auditState.notifyChan != nil {
		c := make(chan struct{}, 1)
		store.auditState.notifyChan <- &bgNotification{
			action:   _BG_DISABLE,
			doneChan: c,
		}
		<-c
		store.auditState.notifyChan = nil
	}
	store.auditState.notifyChanLock.Unlock()
}

func (store *DefaultGroupStore) auditLauncher(notifyChan chan *bgNotification) {
	interval := float64(store.auditState.interval) * float64(time.Second)
	store.randMutex.Lock()
	nextRun := time.Now().Add(time.Duration(interval + interval*store.rand.NormFloat64()*0.1))
	store.randMutex.Unlock()
	var notification *bgNotification
	running := true
	for running {
		if notification == nil {
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
		}
		store.randMutex.Lock()
		nextRun = time.Now().Add(time.Duration(interval + interval*store.rand.NormFloat64()*0.1))
		store.randMutex.Unlock()
		if notification != nil {
			var nextNotification *bgNotification
			switch notification.action {
			case _BG_PASS:
				nextNotification = store.auditPass(true, notifyChan)
			case _BG_DISABLE:
				running = false
			default:
				store.logCritical("audit: invalid action requested: %d", notification.action)
			}
			notification.doneChan <- struct{}{}
			notification = nextNotification
		} else {
			notification = store.auditPass(false, notifyChan)
		}
	}
}

// NOTE: For now, there is no difference between speed=true and speed=false;
// eventually the background audits will try to slow themselves down to finish
// in approximately the store.auditState.interval.
func (store *DefaultGroupStore) auditPass(speed bool, notifyChan chan *bgNotification) *bgNotification {
	if store.logDebug != nil {
		begin := time.Now()
		defer func() {
			store.logDebug("audit: took %s", time.Now().Sub(begin))
		}()
	}
	fp, err := os.Open(store.pathtoc)
	if err != nil {
		store.logError("audit: %s", err)
		return nil
	}
	names, err := fp.Readdirnames(-1)
	fp.Close()
	if err != nil {
		store.logError("audit: %s", err)
		return nil
	}
	shuffledNames := make([]string, len(names))
	store.randMutex.Lock()
	for x, y := range store.rand.Perm(len(names)) {
		shuffledNames[x] = names[y]
	}
	store.randMutex.Unlock()
	names = shuffledNames
	for i := 0; i < len(names); i++ {
		select {
		case notification := <-notifyChan:
			return notification
		default:
		}
		if !strings.HasSuffix(names[i], ".grouptoc") {
			continue
		}
		namets := int64(0)
		if namets, err = strconv.ParseInt(names[i][:len(names[i])-len(".grouptoc")], 10, 64); err != nil {
			store.logError("audit: bad timestamp in name: %#v", names[i])
			continue
		}
		if namets == 0 {
			store.logError("audit: bad timestamp in name: %#v", names[i])
			continue
		}
		if namets == int64(atomic.LoadUint64(&store.activeTOCA)) || namets == int64(atomic.LoadUint64(&store.activeTOCB)) {
			if store.logDebug != nil {
				store.logDebug("audit: skipping current %s", names[i])
			}
			continue
		}
		if namets >= time.Now().UnixNano()-store.auditState.ageThreshold {
			if store.logDebug != nil {
				store.logDebug("audit: skipping young %s", names[i])
			}
			continue
		}
		if store.logDebug != nil {
			store.logDebug("audit: checking %s", names[i])
		}
		failedAudit := uint32(0)
		canceledAudit := uint32(0)
		dataName := names[i][:len(names[i])-3]
		fpr, err := osOpenReadSeeker(path.Join(store.path, dataName))
		if err != nil {
			atomic.AddUint32(&failedAudit, 1)
			if os.IsNotExist(err) {
				if store.logDebug != nil {
					store.logDebug("audit: error opening %s: %s", dataName, err)
				}
			} else {
				store.logError("audit: error opening %s: %s", dataName, err)
			}
		} else {
			corruptions, errs := groupChecksumVerify(fpr)
			closeIfCloser(fpr)
			for _, err := range errs {
				if err != io.EOF && err != io.ErrUnexpectedEOF {
					store.logError("audit: error with %s: %s", dataName, err)
				}
			}
			workers := uint64(1)
			pendingBatchChans := make([]chan []groupTOCEntry, workers)
			freeBatchChans := make([]chan []groupTOCEntry, len(pendingBatchChans))
			for i := 0; i < len(pendingBatchChans); i++ {
				pendingBatchChans[i] = make(chan []groupTOCEntry, 3)
				freeBatchChans[i] = make(chan []groupTOCEntry, cap(pendingBatchChans[i]))
				for j := 0; j < cap(freeBatchChans[i]); j++ {
					freeBatchChans[i] <- make([]groupTOCEntry, store.recoveryBatchSize)
				}
			}
			nextNotificationChan := make(chan *bgNotification, 1)
			controlChan := make(chan struct{})
			go func() {
				select {
				case n := <-notifyChan:
					if atomic.AddUint32(&canceledAudit, 1) == 0 {
						close(controlChan)
					}
					nextNotificationChan <- n
				case <-controlChan:
					nextNotificationChan <- nil
				}
			}()
			wg := &sync.WaitGroup{}
			wg.Add(len(pendingBatchChans))
			for i := 0; i < len(pendingBatchChans); i++ {
				go func(pendingBatchChan chan []groupTOCEntry, freeBatchChan chan []groupTOCEntry) {
					for {
						batch := <-pendingBatchChan
						if batch == nil {
							break
						}
						if atomic.LoadUint32(&failedAudit) == 0 {
							for j := 0; j < len(batch); j++ {
								wr := &batch[j]
								if wr.TimestampBits&_TSB_DELETION != 0 {
									continue
								}
								if groupInCorruptRange(wr.Offset, wr.Length, corruptions) {
									if atomic.AddUint32(&failedAudit, 1) == 0 {
										close(controlChan)
									}
									break
								}
							}
						}
						freeBatchChan <- batch
					}
					wg.Done()
				}(pendingBatchChans[i], freeBatchChans[i])
			}
			fpr, err = osOpenReadSeeker(path.Join(store.pathtoc, names[i]))
			if err != nil {
				atomic.AddUint32(&failedAudit, 1)
				if !os.IsNotExist(err) {
					store.logError("audit: error opening %s: %s", names[i], err)
				}
			} else {
				// NOTE: The block ID is unimportant in this context, so it's
				// just set 1 and ignored elsewhere.
				_, errs := groupReadTOCEntriesBatched(fpr, 1, freeBatchChans, pendingBatchChans, controlChan)
				closeIfCloser(fpr)
				if len(errs) > 0 {
					atomic.AddUint32(&failedAudit, 1)
					for _, err := range errs {
						store.logError("audit: error with %s: %s", names[i], err)
					}
				}
			}
			for i := 0; i < len(pendingBatchChans); i++ {
				pendingBatchChans[i] <- nil
			}
			wg.Wait()
			close(controlChan)
			if n := <-nextNotificationChan; n != nil {
				return n
			}
		}
		if atomic.LoadUint32(&canceledAudit) != 0 {
			if store.logDebug != nil {
				store.logDebug("audit: canceled during %s", names[i])
			}
		} else if atomic.LoadUint32(&failedAudit) == 0 {
			if store.logDebug != nil {
				store.logDebug("audit: passed %s", names[i])
			}
		} else {
			store.logError("audit: failed %s", names[i])
			// TODO: Compaction needs to rewrite all the good entries it can,
			// but also deliberately remove any known bad entries from the
			// locmap so that replication can get them back in place from other
			// servers.
			blockID := store.locBlockIDFromTimestampnano(namets)
			if blockID != 0 {
				result, err := store.compactFile(path.Join(store.pathtoc, names[i]), blockID)
				if store.logDebug != nil {
					store.logDebug("audit: compacted %s (total %d, rewrote %d, stale %d)", names[i], result.count, result.rewrote, result.stale)
				}
				if err != nil {
					store.logError("audit: %s", err)
				}
			}
			if err = os.Remove(path.Join(store.pathtoc, names[i])); err != nil {
				store.logError("audit: unable to remove %s: %s", names[i], err)
			}
			if err = os.Remove(path.Join(store.path, names[i][:len(names[i])-len("toc")])); err != nil {
				store.logError("audit: unable to remove %s: %s", names[i][:len(names[i])-len("toc")], err)
			}
			if err = store.closeLocBlock(blockID); err != nil {
				store.logError("audit: error closing in-memory block for %s: %s", names[i], err)
			}
			go func() {
				store.logError("audit: all audit actions require store restarts at this time.")
				store.DisableAll()
				store.Flush()
				store.restartChan <- errors.New("audit failure occurred requiring a restart")
			}()
			return &bgNotification{
				action:   _BG_DISABLE,
				doneChan: make(chan struct{}, 1),
			}
		}
	}
	return nil
}
