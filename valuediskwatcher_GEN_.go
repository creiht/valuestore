package valuestore

import (
	"time"

	"github.com/ricochet2200/go-disk-usage/du"
)

type valueDiskWatcherState struct {
	freeDisableThreshold   uint64
	freeReenableThreshold  uint64
	usageDisableThreshold  float32
	usageReenableThreshold float32
	free                   uint64
	used                   uint64
	size                   uint64
	freetoc                uint64
	usedtoc                uint64
	sizetoc                uint64
}

func (vs *DefaultValueStore) diskWatcherConfig(cfg *ValueStoreConfig) {
	vs.diskWatcherState.freeDisableThreshold = cfg.FreeDisableThreshold
	vs.diskWatcherState.freeReenableThreshold = cfg.FreeReenableThreshold
	vs.diskWatcherState.usageDisableThreshold = cfg.UsageDisableThreshold
	vs.diskWatcherState.usageReenableThreshold = cfg.UsageReenableThreshold
}

func (vs *DefaultValueStore) diskWatcherLaunch() {
	go vs.diskWatcher()
}

func (vs *DefaultValueStore) diskWatcher() {
	disabled := false
	for {
		time.Sleep(time.Minute)
		u := du.NewDiskUsage(vs.path)
		utoc := u
		if vs.pathtoc != vs.path {
			utoc = du.NewDiskUsage(vs.pathtoc)
		}
		vs.diskWatcherState.free = u.Free()
		vs.diskWatcherState.used = u.Used()
		vs.diskWatcherState.size = u.Size()
		usage := u.Usage()
		vs.diskWatcherState.freetoc = utoc.Free()
		vs.diskWatcherState.usedtoc = utoc.Used()
		vs.diskWatcherState.sizetoc = utoc.Size()
		usagetoc := utoc.Usage()
		if disabled {
			if (vs.diskWatcherState.freeReenableThreshold == 0 || (vs.diskWatcherState.free >= vs.diskWatcherState.freeReenableThreshold && vs.diskWatcherState.freetoc >= vs.diskWatcherState.freeReenableThreshold)) && (vs.diskWatcherState.usageReenableThreshold == 0 || (usage <= vs.diskWatcherState.usageReenableThreshold && usagetoc <= vs.diskWatcherState.usageReenableThreshold)) {
				vs.logCritical("passed the free/usage threshold for automatic re-enabling\n")
				vs.enableWrites(false) // false indicates non-user call
				disabled = false
			}
		} else {
			if (vs.diskWatcherState.freeDisableThreshold != 0 && (vs.diskWatcherState.free <= vs.diskWatcherState.freeDisableThreshold || vs.diskWatcherState.freetoc <= vs.diskWatcherState.freeDisableThreshold)) || (vs.diskWatcherState.usageDisableThreshold != 0 && (usage >= vs.diskWatcherState.usageDisableThreshold || usagetoc >= vs.diskWatcherState.usageDisableThreshold)) {
				vs.logCritical("passed the free/usage threshold for automatic disabling\n")
				vs.disableWrites(false) // false indicates non-user call
				disabled = true
			}
		}
	}
}