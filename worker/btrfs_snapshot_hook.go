//go:build linux
// +build linux

package worker

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/dennwc/btrfs"
)

type btrfsSnapshotHook struct {
	provider           mirrorProvider
	mirrorSnapshotPath string
}

// the user who runs the jobs (typically `tunasync`) should be granted the permission to run btrfs commands
// TODO: check if the filesystem is Btrfs
func newBtrfsSnapshotHook(provider mirrorProvider, snapshotPath string, mirror mirrorConfig) *btrfsSnapshotHook {
	mirrorSnapshotPath := mirror.SnapshotPath
	if mirrorSnapshotPath == "" {
		mirrorSnapshotPath = filepath.Join(snapshotPath, provider.Name())
	}
	return &btrfsSnapshotHook{
		provider:           provider,
		mirrorSnapshotPath: mirrorSnapshotPath,
	}
}

// check if path `snapshotPath/providerName` exists
// Case 1: Not exists => create a new subvolume
// Case 2: Exists as a subvolume => nothing to do
// Case 3: Exists as a directory => error detected
func (h *btrfsSnapshotHook) preJob() error {
	path := h.provider.WorkingDir()
	if _, err := os.Stat(path); os.IsNotExist(err) {
		// create subvolume
		err := btrfs.CreateSubVolume(path)
		if err != nil {
			logger.Errorf("未能创建 Btrfs 子卷 %s: %s", path, err.Error())
			return err
		}
		logger.Noticef("创建了新的 Btrfs 子卷 %s", path)
	} else {
		if is, err := btrfs.IsSubVolume(path); err != nil {
			return err
		} else if !is {
			return fmt.Errorf("路径 %s 存在但不是 Btrfs 子卷", path)
		}
	}
	return nil
}

func (h *btrfsSnapshotHook) preExec() error {
	return nil
}

func (h *btrfsSnapshotHook) postExec() error {
	return nil
}

// delete old snapshot if exists, then create a new snapshot
func (h *btrfsSnapshotHook) postSuccess() error {
	if _, err := os.Stat(h.mirrorSnapshotPath); !os.IsNotExist(err) {
		isSubVol, err := btrfs.IsSubVolume(h.mirrorSnapshotPath)
		if err != nil {
			return err
		} else if !isSubVol {
			return fmt.Errorf("路径 %s 存在且不是 Btrfs 快照", h.mirrorSnapshotPath)
		}
		// is old snapshot => delete it
		if err := btrfs.DeleteSubVolume(h.mirrorSnapshotPath); err != nil {
			logger.Errorf("未能删除旧的 Btrfs 快照 %s", h.mirrorSnapshotPath)
			return err
		}
		logger.Noticef("删除旧快照 %s", h.mirrorSnapshotPath)
	}
	// create a new writable snapshot
	// (the snapshot is writable so that it can be deleted easily)
	if err := btrfs.SnapshotSubVolume(h.provider.WorkingDir(), h.mirrorSnapshotPath, false); err != nil {
		logger.Errorf("未能创建新的 Btrfs 快照 %s", h.mirrorSnapshotPath)
		return err
	}
	logger.Noticef("创建了新的 Btrfs 快照 %s", h.mirrorSnapshotPath)
	return nil
}

// keep the old snapshot => nothing to do
func (h *btrfsSnapshotHook) postFail() error {
	return nil
}
