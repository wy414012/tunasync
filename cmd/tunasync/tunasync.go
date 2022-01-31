package main

import (
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/moby/moby/pkg/reexec"
	"github.com/pkg/profile"
	"github.com/urfave/cli"
	"gopkg.in/op/go-logging.v1"

	tunasync "github.com/tuna/tunasync/internal"
	"github.com/tuna/tunasync/manager"
	"github.com/tuna/tunasync/worker"
)

var (
	buildstamp = ""
	githash    = "没有提供 gitash"
)

var logger = logging.MustGetLogger("tunasync")

func startManager(c *cli.Context) error {
	tunasync.InitLogger(c.Bool("verbose"), c.Bool("debug"), c.Bool("with-systemd"))

	cfg, err := manager.LoadConfig(c.String("config"), c)
	if err != nil {
		logger.Errorf("加载配置时出错: %s", err.Error())
		os.Exit(1)
	}
	if !cfg.Debug {
		gin.SetMode(gin.ReleaseMode)
	}

	m := manager.GetTUNASyncManager(cfg)
	if m == nil {
		logger.Errorf("初始化 TUNA 同步工作程序时出错。")
		os.Exit(1)
	}

	logger.Info("运行 tunasync 管理器服务器。")
	m.Run()
	return nil
}

func startWorker(c *cli.Context) error {
	tunasync.InitLogger(c.Bool("verbose"), c.Bool("debug"), c.Bool("with-systemd"))
	if !c.Bool("debug") {
		gin.SetMode(gin.ReleaseMode)
	}

	cfg, err := worker.LoadConfig(c.String("config"))
	if err != nil {
		logger.Errorf("加载配置时出错: %s", err.Error())
		os.Exit(1)
	}

	w := worker.NewTUNASyncWorker(cfg)
	if w == nil {
		logger.Errorf("初始化 TUNA 同步工作程序时出错。")
		os.Exit(1)
	}

	if profPath := c.String("prof-path"); profPath != "" {
		valid := false
		if fi, err := os.Stat(profPath); err == nil {
			if fi.IsDir() {
				valid = true
				defer profile.Start(profile.ProfilePath(profPath)).Stop()
			}
		}
		if !valid {
			logger.Errorf("分析路径无效: %s", profPath)
			os.Exit(1)
		}
	}

	go func() {
		time.Sleep(1 * time.Second)
		sigChan := make(chan os.Signal, 1)
		signal.Notify(sigChan, syscall.SIGHUP)
		signal.Notify(sigChan, syscall.SIGINT)
		signal.Notify(sigChan, syscall.SIGTERM)
		for s := range sigChan {
			switch s {
			case syscall.SIGHUP:
				logger.Info("收到重载信号")
				newCfg, err := worker.LoadConfig(c.String("config"))
				if err != nil {
					logger.Errorf("加载配置时出错: %s", err.Error())
				} else {
					w.ReloadMirrorConfig(newCfg.Mirrors)
				}
			case syscall.SIGINT, syscall.SIGTERM:
				w.Halt()
			}
		}
	}()

	logger.Info("运行 tunasync 工作程序。")
	w.Run()
	return nil
}

func main() {

	if reexec.Init() {
		return
	}

	cli.VersionPrinter = func(c *cli.Context) {
		var builddate string
		if buildstamp == "" {
			builddate = "未提供构建日期"
		} else {
			ts, err := strconv.Atoi(buildstamp)
			if err != nil {
				builddate = "未提供构建日期"
			} else {
				t := time.Unix(int64(ts), 0)
				builddate = t.String()
			}
		}
		fmt.Printf(
			"版本: %s\n"+
				"GitHash值: %s\n"+
				"编译时间: %s\n",
			c.App.Version, githash, builddate,
		)
	}

	app := cli.NewApp()
	app.Name = "tunasync"
	app.Usage = "tunasync 镜像作业管理工具"
	app.EnableBashCompletion = true
	app.Version = tunasync.Version
	app.Commands = []cli.Command{
		{
			Name:    "manager",
			Aliases: []string{"m"},
			Usage:   "启动 tunasync 管理器",
			Action:  startManager,
			Flags: []cli.Flag{
				cli.StringFlag{
					Name:  "config, c",
					Usage: "从加载管理器配置 `FILE`",
				},
				cli.StringFlag{
					Name:  "addr",
					Usage: "管理程序监听地址 `ADDR`",
				},
				cli.StringFlag{
					Name:  "port",
					Usage: "管理程序监听端口 `PORT`",
				},
				cli.StringFlag{
					Name:  "cert",
					Usage: "使用管理器 SSL 证书 `FILE`",
				},
				cli.StringFlag{
					Name:  "key",
					Usage: "使用管理器 SSL 密钥 `FILE`",
				},
				cli.StringFlag{
					Name:  "db-file",
					Usage: "使用 `FILE` 作为数据库文件",
				},
				cli.StringFlag{
					Name:  "db-type",
					Usage: "使用数据库类型 `TYPE`",
				},
				cli.BoolFlag{
					Name:  "verbose, v",
					Usage: "启用详细日志记录",
				},
				cli.BoolFlag{
					Name:  "debug",
					Usage: "在调试模式下运行管理器",
				},
				cli.BoolFlag{
					Name:  "with-systemd",
					Usage: "启用与 systemd 兼容的日志记录",
				},
				cli.StringFlag{
					Name:  "pidfile",
					Value: "/run/tunasync/tunasync.manager.pid",
					Usage: "manager进程的pid文件",
				},
			},
		},
		{
			Name:    "worker",
			Aliases: []string{"w"},
			Usage:   "运行 tunasync 工作程序。",
			Action:  startWorker,
			Flags: []cli.Flag{
				cli.StringFlag{
					Name:  "config, c",
					Usage: "从`FILE`加载工作器配置",
				},
				cli.BoolFlag{
					Name:  "verbose, v",
					Usage: "启用详细日志记录",
				},
				cli.BoolFlag{
					Name:  "debug",
					Usage: "在调试模式下运行工作程序",
				},
				cli.BoolFlag{
					Name:  "with-systemd",
					Usage: "启用与 systemd 兼容的日志记录",
				},
				cli.StringFlag{
					Name:  "pidfile",
					Value: "/run/tunasync/tunasync.worker.pid",
					Usage: "工作程序进程的pid文件",
				},
				cli.StringFlag{
					Name:  "prof-path",
					Value: "",
					Usage: "分析文件路径",
				},
			},
		},
	}
	app.Run(os.Args)
}
