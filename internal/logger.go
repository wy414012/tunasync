package internal

import (
	"os"

	"gopkg.in/op/go-logging.v1"
)

// 初始化日志记录格式和级别
func InitLogger(verbose, debug, withSystemd bool) {
	var fmtString string
	if withSystemd {
		fmtString = "[%{level:.6s}] %{message}"
	} else {
		if debug {
			fmtString = "%{color}[%{time:06-01-02 15:04:05}][%{level:.6s}][%{shortfile}]%{color:reset} %{message}"
		} else {
			fmtString = "%{color}[%{time:06-01-02 15:04:05}][%{level:.6s}]%{color:reset} %{message}"
		}
	}
	format := logging.MustStringFormatter(fmtString)
	logging.SetFormatter(format)
	logging.SetBackend(logging.NewLogBackend(os.Stdout, "", 0))

	if debug {
		logging.SetLevel(logging.DEBUG, "tunasync")
		logging.SetLevel(logging.DEBUG, "tunasynctl")
	} else if verbose {
		logging.SetLevel(logging.INFO, "tunasync")
		logging.SetLevel(logging.INFO, "tunasynctl")
	} else {
		logging.SetLevel(logging.NOTICE, "tunasync")
		logging.SetLevel(logging.NOTICE, "tunasynctl")
	}
}
