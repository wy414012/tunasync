package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"strconv"
	"strings"
	"text/template"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/urfave/cli"
	"gopkg.in/op/go-logging.v1"

	tunasync "github.com/tuna/tunasync/internal"
)

var (
	buildstamp = ""
	githash    = "没有提供 gitash"
)

const (
	listJobsPath      = "/jobs"
	listWorkersPath   = "/workers"
	flushDisabledPath = "/jobs/disabled"
	cmdPath           = "/cmd"

	systemCfgFile = "$HOME/.config/tunasync/ctl.conf" // system-wide conf
	userCfgFile   = "$HOME/.config/tunasync/ctl.conf" // user-specific conf
)

var logger = logging.MustGetLogger("tunasynctl")

var baseURL string
var client *http.Client

func initializeWrapper(handler cli.ActionFunc) cli.ActionFunc {
	return func(c *cli.Context) error {
		err := initialize(c)
		if err != nil {
			return cli.NewExitError(err.Error(), 1)
		}
		return handler(c)
	}
}

type config struct {
	ManagerAddr string `toml:"manager_addr"`
	ManagerPort int    `toml:"manager_port"`
	CACert      string `toml:"ca_cert"`
}

func loadConfig(cfgFile string, cfg *config) error {
	if cfgFile != "" {
		logger.Infof("加载配置: %s", cfgFile)
		if _, err := toml.DecodeFile(cfgFile, cfg); err != nil {
			// logger.Errorf(err.Error())
			return err
		}
	}

	return nil
}

func initialize(c *cli.Context) error {
	// init logger
	tunasync.InitLogger(c.Bool("verbose"), c.Bool("debug"), false)

	cfg := new(config)

	// 默认配置
	cfg.ManagerAddr = "localhost"
	cfg.ManagerPort = 14242

	// 找到配置文件并加载配置
	if _, err := os.Stat(systemCfgFile); err == nil {
		err = loadConfig(systemCfgFile, cfg)
		if err != nil {
			return err
		}
	}
	logger.Debug("用户配置文件: %s", os.ExpandEnv(userCfgFile))
	if _, err := os.Stat(os.ExpandEnv(userCfgFile)); err == nil {
		err = loadConfig(os.ExpandEnv(userCfgFile), cfg)
		if err != nil {
			return err
		}
	}
	if c.String("config") != "" {
		err := loadConfig(c.String("config"), cfg)
		if err != nil {
			return err
		}
	}

	// override config using the command-line arguments
	if c.String("manager") != "" {
		cfg.ManagerAddr = c.String("manager")
	}
	if c.Int("port") > 0 {
		cfg.ManagerPort = c.Int("port")
	}

	if c.String("ca-cert") != "" {
		cfg.CACert = c.String("ca-cert")
	}

	// 解析管理服务器的基本 url
	if cfg.CACert != "" {
		baseURL = fmt.Sprintf("https://%s:%d", cfg.ManagerAddr, cfg.ManagerPort)
	} else {
		baseURL = fmt.Sprintf("http://%s:%d", cfg.ManagerAddr, cfg.ManagerPort)
	}

	logger.Infof("使用管理器地址: %s", baseURL)

	// create HTTP client
	var err error
	client, err = tunasync.CreateHTTPClient(cfg.CACert)
	if err != nil {
		err = fmt.Errorf("初始化 HTTP 客户端时出错: %s", err.Error())
		// logger.Error(err.Error())
		return err

	}
	return nil
}

func listWorkers(c *cli.Context) error {
	var workers []tunasync.WorkerStatus
	_, err := tunasync.GetJSON(baseURL+listWorkersPath, &workers, client)
	if err != nil {
		return cli.NewExitError(
			fmt.Sprintf("无法正确获取信息"+
				"管理服务器: %s", err.Error()), 1)
	}

	b, err := json.MarshalIndent(workers, "", "  ")
	if err != nil {
		return cli.NewExitError(
			fmt.Sprintf("打印信息时出错: %s",
				err.Error()),
			1)
	}
	fmt.Println(string(b))
	return nil
}

func listJobs(c *cli.Context) error {
	var genericJobs interface{}
	if c.Bool("all") {
		var jobs []tunasync.WebMirrorStatus
		_, err := tunasync.GetJSON(baseURL+listJobsPath, &jobs, client)
		if err != nil {
			return cli.NewExitError(
				fmt.Sprintf("未能正确获取信息 "+
					"来自管理服务器的所有作业: %s", err.Error()),
				1)
		}
		if statusStr := c.String("status"); statusStr != "" {
			filteredJobs := make([]tunasync.WebMirrorStatus, 0, len(jobs))
			var statuses []tunasync.SyncStatus
			for _, s := range strings.Split(statusStr, ",") {
				var status tunasync.SyncStatus
				err = status.UnmarshalJSON([]byte("\"" + strings.TrimSpace(s) + "\""))
				if err != nil {
					return cli.NewExitError(
						fmt.Sprintf("解析状态错误: %s", err.Error()),
						1)
				}
				statuses = append(statuses, status)
			}
			for _, job := range jobs {
				for _, s := range statuses {
					if job.Status == s {
						filteredJobs = append(filteredJobs, job)
						break
					}
				}
			}
			genericJobs = filteredJobs
		} else {
			genericJobs = jobs
		}
	} else {
		var jobs []tunasync.MirrorStatus
		args := c.Args()
		if len(args) == 0 {
			return cli.NewExitError(
				fmt.Sprintf("使用错误：作业命令需要在"+
					" 至少一个ID或 \"--all\" 标识"), 1)
		}
		ans := make(chan []tunasync.MirrorStatus, len(args))
		for _, workerID := range args {
			go func(workerID string) {
				var workerJobs []tunasync.MirrorStatus
				_, err := tunasync.GetJSON(fmt.Sprintf("%s/工人/%s/工作",
					baseURL, workerID), &workerJobs, client)
				if err != nil {
					logger.Infof("未能正确获得工作"+
						" 工人用 %s: %s", workerID, err.Error())
				}
				ans <- workerJobs
			}(workerID)
		}
		for range args {
			job := <-ans
			if job == nil {
				return cli.NewExitError(
					fmt.Sprintf("未能正确获取信息 "+
						"来至至少一个管理器的工作"),
					1)
			}
			jobs = append(jobs, job...)
		}
		genericJobs = jobs
	}

	if format := c.String("format"); format != "" {
		tpl := template.New("")
		_, err := tpl.Parse(format)
		if err != nil {
			return cli.NewExitError(
				fmt.Sprintf("解析格式模板时出错: %s", err.Error()),
				1)
		}
		switch jobs := genericJobs.(type) {
		case []tunasync.WebMirrorStatus:
			for _, job := range jobs {
				err = tpl.Execute(os.Stdout, job)
				if err != nil {
					return cli.NewExitError(
						fmt.Sprintf("打印信息时出错: %s", err.Error()),
						1)
				}
				fmt.Println()
			}
		case []tunasync.MirrorStatus:
			for _, job := range jobs {
				err = tpl.Execute(os.Stdout, job)
				if err != nil {
					return cli.NewExitError(
						fmt.Sprintf("打印信息时出错: %s", err.Error()),
						1)
				}
				fmt.Println()
			}
		}
	} else {
		b, err := json.MarshalIndent(genericJobs, "", "  ")
		if err != nil {
			return cli.NewExitError(
				fmt.Sprintf("打印信息时出错: %s", err.Error()),
				1)
		}
		fmt.Println(string(b))
	}

	return nil
}

func updateMirrorSize(c *cli.Context) error {
	args := c.Args()
	if len(args) != 2 {
		return cli.NewExitError("用法: tunasynctl -w <worker-id> <mirror> <size>", 1)
	}
	workerID := c.String("worker")
	mirrorID := args.Get(0)
	mirrorSize := args.Get(1)

	msg := struct {
		Name string `json:"name"`
		Size string `json:"size"`
	}{
		Name: mirrorID,
		Size: mirrorSize,
	}

	url := fmt.Sprintf(
		"%s/workers/%s/jobs/%s/size", baseURL, workerID, mirrorID,
	)

	resp, err := tunasync.PostJSON(url, msg, client)
	if err != nil {
		return cli.NewExitError(
			fmt.Sprintf("未能向管理服务器发出请求: %s",
				err.Error()),
			1)
	}
	defer resp.Body.Close()
	body, _ := ioutil.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return cli.NewExitError(
			fmt.Sprintf("管理服务器更新镜像大小失败: %s", body), 1,
		)
	}

	var status tunasync.MirrorStatus
	json.Unmarshal(body, &status)
	if status.Size != mirrorSize {
		return cli.NewExitError(
			fmt.Sprintf(
				"镜子尺寸错误，等待 %s, 管理器运行 %s",
				mirrorSize, status.Size,
			), 1,
		)
	}

	fmt.Printf("已成功将镜像大小更新为：%s\n", mirrorSize)
	return nil
}

func removeWorker(c *cli.Context) error {
	args := c.Args()
	if len(args) != 0 {
		return cli.NewExitError("用法: tunasynctl -w <worker-id>", 1)
	}
	workerID := c.String("worker")
	if len(workerID) == 0 {
		return cli.NewExitError("请指定 <worker-id>", 1)
	}
	url := fmt.Sprintf("%s/workers/%s", baseURL, workerID)

	req, err := http.NewRequest("DELETE", url, nil)
	if err != nil {
		logger.Panicf("无效的 HTTP 请求: %s", err.Error())
	}
	resp, err := client.Do(req)

	if err != nil {
		return cli.NewExitError(
			fmt.Sprintf("未能向管理服务器发出请求: %s", err.Error()), 1)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, err := ioutil.ReadAll(resp.Body)
		if err != nil {
			return cli.NewExitError(
				fmt.Sprintf("解析响应失败: %s", err.Error()),
				1)
		}

		return cli.NewExitError(fmt.Sprintf("未能正确发送"+
			" 命令：HTTP 状态码不是 200: %s", body),
			1)
	}

	res := map[string]string{}
	err = json.NewDecoder(resp.Body).Decode(&res)
	if res["message"] == "deleted" {
		fmt.Println("成功移除工作流")
	} else {
		return cli.NewExitError("无法移除工作流", 1)
	}
	return nil
}

func flushDisabledJobs(c *cli.Context) error {
	req, err := http.NewRequest("DELETE", baseURL+flushDisabledPath, nil)
	if err != nil {
		logger.Panicf("无效的 HTTP 请求: %s", err.Error())
	}
	resp, err := client.Do(req)

	if err != nil {
		return cli.NewExitError(
			fmt.Sprintf("未能向管理服务器发出请求: %s",
				err.Error()),
			1)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, err := ioutil.ReadAll(resp.Body)
		if err != nil {
			return cli.NewExitError(
				fmt.Sprintf("解析响应失败: %s", err.Error()),
				1)
		}

		return cli.NewExitError(fmt.Sprintf("未能正确发送"+
			" 命令：HTTP 状态码不是 200: %s", body),
			1)
	}

	fmt.Println("成功刷新禁用的作业")
	return nil
}

func cmdJob(cmd tunasync.CmdVerb) cli.ActionFunc {
	return func(c *cli.Context) error {
		var mirrorID string
		var argsList []string
		if len(c.Args()) == 1 {
			mirrorID = c.Args()[0]
		} else if len(c.Args()) == 2 {
			mirrorID = c.Args()[0]
			for _, arg := range strings.Split(c.Args()[1], ",") {
				argsList = append(argsList, strings.TrimSpace(arg))
			}
		} else {
			return cli.NewExitError("使用错误：cmd 命令只接收 "+
				"1 个必需的位置参数 MIRROR 和 1 个可选的 "+
				"参数 WORKER", 1)
		}

		options := map[string]bool{}
		if c.Bool("force") {
			options["force"] = true
		}
		cmd := tunasync.ClientCmd{
			Cmd:      cmd,
			MirrorID: mirrorID,
			WorkerID: c.String("worker"),
			Args:     argsList,
			Options:  options,
		}
		resp, err := tunasync.PostJSON(baseURL+cmdPath, cmd, client)
		if err != nil {
			return cli.NewExitError(
				fmt.Sprintf("未能正确发送命令: %s",
					err.Error()),
				1)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			body, err := ioutil.ReadAll(resp.Body)
			if err != nil {
				return cli.NewExitError(
					fmt.Sprintf("解析响应失败: %s", err.Error()),
					1)
			}

			return cli.NewExitError(fmt.Sprintf("未能正确发送"+
				" 命令：HTTP 状态码不是 200: %s", body),
				1)
		}
		fmt.Println("成功发送命令")

		return nil
	}
}

func cmdWorker(cmd tunasync.CmdVerb) cli.ActionFunc {
	return func(c *cli.Context) error {

		if c.String("worker") == "" {
			return cli.NewExitError("请使用 -w 指定工作流ID<worker-id>", 1)
		}

		cmd := tunasync.ClientCmd{
			Cmd:      cmd,
			WorkerID: c.String("worker"),
		}
		resp, err := tunasync.PostJSON(baseURL+cmdPath, cmd, client)
		if err != nil {
			return cli.NewExitError(
				fmt.Sprintf("未能正确发送命令: %s",
					err.Error()),
				1)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			body, err := ioutil.ReadAll(resp.Body)
			if err != nil {
				return cli.NewExitError(
					fmt.Sprintf("解析响应失败: %s", err.Error()),
					1)
			}

			return cli.NewExitError(fmt.Sprintf("未能正确发送"+
				" 命令：HTTP 状态码不是 200: %s", body),
				1)
		}
		fmt.Println("成功发送命令")

		return nil
	}
}

func main() {
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
	app.EnableBashCompletion = true
	app.Version = tunasync.Version
	app.Name = "tunasynctl"
	app.Usage = "control client for tunasync manager"

	commonFlags := []cli.Flag{
		cli.StringFlag{
			Name: "config, c",
			Usage: "读取配置`FILE` 而不是" +
				" ~/.config/tunasync/ctl.conf 和 /etc/tunasync/ctl.conf",
		},
		cli.StringFlag{
			Name:  "manager, m",
			Usage: "管理服务器地址",
		},
		cli.StringFlag{
			Name:  "port, p",
			Usage: "管理服务器端口",
		},
		cli.StringFlag{
			Name:  "ca-cert",
			Usage: "信任根 CA 证书文件`CERT`",
		},

		cli.BoolFlag{
			Name:  "verbose, v",
			Usage: "启用详细日志记录",
		},
		cli.BoolFlag{
			Name:  "debug",
			Usage: "启用调试日志记录",
		},
	}
	cmdFlags := []cli.Flag{
		cli.StringFlag{
			Name:  "worker, w",
			Usage: "发送命令到 `WORKER`",
		},
	}

	forceStartFlag := cli.BoolFlag{
		Name:  "force, f",
		Usage: "覆盖并发限制",
	}

	app.Commands = []cli.Command{
		{
			Name:  "list",
			Usage: "列出工人的工作",
			Flags: append(commonFlags,
				[]cli.Flag{
					cli.BoolFlag{
						Name:  "all, a",
						Usage: "列出所有工人的所有工作",
					},
					cli.StringFlag{
						Name:  "status, s",
						Usage: "根据提供的状态过滤输出",
					},
					cli.StringFlag{
						Name:  "format, f",
						Usage: "使用 Go 模板打印漂亮的容器",
					},
				}...),
			Action: initializeWrapper(listJobs),
		},
		{
			Name:   "flush",
			Usage:  "刷新禁用的作业",
			Flags:  commonFlags,
			Action: initializeWrapper(flushDisabledJobs),
		},
		{
			Name:   "workers",
			Usage:  "列出工作流",
			Flags:  commonFlags,
			Action: initializeWrapper(listWorkers),
		},
		{
			Name:  "rm-worker",
			Usage: "删除工作流",
			Flags: append(
				commonFlags,
				cli.StringFlag{
					Name:  "worker, w",
					Usage: "要删除的工作流ID",
				},
			),
			Action: initializeWrapper(removeWorker),
		},
		{
			Name:  "set-size",
			Usage: "设置镜像大小",
			Flags: append(
				commonFlags,
				cli.StringFlag{
					Name:  "worker, w",
					Usage: "指定镜像作业的worker-id",
				},
			),
			Action: initializeWrapper(updateMirrorSize),
		},
		{
			Name:   "start",
			Usage:  "启动工作流",
			Flags:  append(append(commonFlags, cmdFlags...), forceStartFlag),
			Action: initializeWrapper(cmdJob(tunasync.CmdStart)),
		},
		{
			Name:   "stop",
			Usage:  "停止工作流",
			Flags:  append(commonFlags, cmdFlags...),
			Action: initializeWrapper(cmdJob(tunasync.CmdStop)),
		},
		{
			Name:   "disable",
			Usage:  "禁用工作流",
			Flags:  append(commonFlags, cmdFlags...),
			Action: initializeWrapper(cmdJob(tunasync.CmdDisable)),
		},
		{
			Name:   "restart",
			Usage:  "重启工作流",
			Flags:  append(commonFlags, cmdFlags...),
			Action: initializeWrapper(cmdJob(tunasync.CmdRestart)),
		},
		{
			Name:   "reload",
			Usage:  "重新加载工作流配置",
			Flags:  append(commonFlags, cmdFlags...),
			Action: initializeWrapper(cmdWorker(tunasync.CmdReload)),
		},
		{
			Name:   "ping",
			Usage:  "测试工作流",
			Flags:  append(commonFlags, cmdFlags...),
			Action: initializeWrapper(cmdJob(tunasync.CmdPing)),
		},
	}
	app.Run(os.Args)
}
