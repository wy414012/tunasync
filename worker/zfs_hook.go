package worker

import (
	"fmt"
	"os"
	"os/user"
	"strings"

	"github.com/codeskyblue/go-sh"
)

type zfsHook struct {
	emptyHook
	zpool string
}

func newZfsHook(provider mirrorProvider, zpool string) *zfsHook {
	return &zfsHook{
		emptyHook: emptyHook{
			provider: provider,
		},
		zpool: zpool,
	}
}

func (z *zfsHook) printHelpMessage() {
	zfsDataset := fmt.Sprintf("%s/%s", z.zpool, z.provider.Name())
	zfsDataset = strings.ToLower(zfsDataset)
	workingDir := z.provider.WorkingDir()
	logger.Infof("您可以使用创建 ZFS 数据集:")
	logger.Infof("    zfs 创建 '%s'", zfsDataset)
	logger.Infof("    zfs 设置挂载点='%s' '%s'", workingDir, zfsDataset)
	usr, err := user.Current()
	if err != nil || usr.Uid == "0" {
		return
	}
	logger.Infof("    chown %s '%s'", usr.Uid, workingDir)
}

// check if working directory is a zfs dataset
func (z *zfsHook) preJob() error {
	workingDir := z.provider.WorkingDir()
	if _, err := os.Stat(workingDir); os.IsNotExist(err) {
		logger.Errorf("Directory %s doesn't exist", workingDir)
		z.printHelpMessage()
		return err
	}
	if err := sh.Command("mountpoint", "-q", workingDir).Run(); err != nil {
		logger.Errorf("%s is not a mount point", workingDir)
		z.printHelpMessage()
		return err
	}
	return nil
}
