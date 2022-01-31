package internal

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"net/http"
	"os/exec"
	"regexp"
	"time"
)

var rsyncExitValues = map[int]string{
	0:  "成功",
	1:  "语法或用法错误",
	2:  "协议不兼容",
	3:  "选择输入/输出文件、目录时出错",
	4:  "不支持请求的操作：试图在不支持的平台上操作 64 位文件； 或者指定了客户端支持而不是服务器支持的选项。",
	5:  "启动客户端-服务器协议时出错",
	6:  "守护进程无法附加到日志文件",
	10: "套接字 I/O 错误",
	11: "文件 I/O 错误",
	12: "rsync 协议数据流错误",
	13: "程序诊断错误",
	14: "IPC 代码错误",
	20: "收到 SIGUSR1 或 SIGINT",
	21: "waitpid() 返回一些错误",
	22: "分配核心内存缓冲区时出错",
	23: "由于错误导致部分转移",
	24: "由于源文件消失而导致部分传输",
	25: "--max-delete 限制停止删除",
	30: "数据发送/接收超时",
	35: "等待守护进程连接超时",
}

// GetTLSConfig generate tls.Config from CAFile
func GetTLSConfig(CAFile string) (*tls.Config, error) {
	caCert, err := ioutil.ReadFile(CAFile)
	if err != nil {
		return nil, err
	}
	caCertPool := x509.NewCertPool()
	if ok := caCertPool.AppendCertsFromPEM(caCert); !ok {
		return nil, errors.New("无法将 CA 添加到池中")
	}

	tlsConfig := &tls.Config{
		RootCAs: caCertPool,
	}
	tlsConfig.BuildNameToCertificate()
	return tlsConfig, nil
}

// CreateHTTPClient returns a http.Client
func CreateHTTPClient(CAFile string) (*http.Client, error) {
	var tlsConfig *tls.Config
	var err error

	if CAFile != "" {
		tlsConfig, err = GetTLSConfig(CAFile)
		if err != nil {
			return nil, err
		}
	}

	tr := &http.Transport{
		MaxIdleConnsPerHost: 20,
		TLSClientConfig:     tlsConfig,
	}

	return &http.Client{
		Transport: tr,
		Timeout:   5 * time.Second,
	}, nil
}

// PostJSON posts json object to url
func PostJSON(url string, obj interface{}, client *http.Client) (*http.Response, error) {
	if client == nil {
		client, _ = CreateHTTPClient("")
	}
	b := new(bytes.Buffer)
	if err := json.NewEncoder(b).Encode(obj); err != nil {
		return nil, err
	}
	return client.Post(url, "application/json; charset=utf-8", b)
}

// GetJSON gets a json response from url
func GetJSON(url string, obj interface{}, client *http.Client) (*http.Response, error) {
	if client == nil {
		client, _ = CreateHTTPClient("")
	}

	resp, err := client.Get(url)
	if err != nil {
		return resp, err
	}
	if resp.StatusCode != http.StatusOK {
		return resp, errors.New("HTTP 状态码不是 200")
	}
	defer resp.Body.Close()
	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return resp, err
	}
	return resp, json.Unmarshal(body, obj)
}

// FindAllSubmatchInFile calls re.FindAllSubmatch to find matches in given file
func FindAllSubmatchInFile(fileName string, re *regexp.Regexp) (matches [][][]byte, err error) {
	if fileName == "/dev/null" {
		err = errors.New("无效的日志文件")
		return
	}
	if content, err := ioutil.ReadFile(fileName); err == nil {
		matches = re.FindAllSubmatch(content, -1)
		// fmt.Printf("FindAllSubmatchInFile: %q\n", matches)
	}
	return
}

// ExtractSizeFromLog uses a regexp to extract the size from log files
func ExtractSizeFromLog(logFile string, re *regexp.Regexp) string {
	matches, _ := FindAllSubmatchInFile(logFile, re)
	if matches == nil || len(matches) == 0 {
		return ""
	}
	// return the first capture group of the last occurrence
	return string(matches[len(matches)-1][1])
}

// ExtractSizeFromRsyncLog extracts the size from rsync logs
func ExtractSizeFromRsyncLog(logFile string) string {
	// (?m) flag enables multi-line mode
	re := regexp.MustCompile(`(?m)^Total file size: ([0-9\.]+[KMGTP]?) bytes`)
	return ExtractSizeFromLog(logFile, re)
}

// TranslateRsyncErrorCode translates the exit code of rsync to a message
func TranslateRsyncErrorCode(cmdErr error) (exitCode int, msg string) {

	if exiterr, ok := cmdErr.(*exec.ExitError); ok {
		exitCode = exiterr.ExitCode()
		strerr, valid := rsyncExitValues[exitCode]
		if valid {
			msg = fmt.Sprintf("rsync 错误: %s", strerr)
		}
	}
	return
}
