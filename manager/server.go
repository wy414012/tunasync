package manager

import (
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"

	. "github.com/tuna/tunasync/internal"
)

const (
	_errorKey = "error"
	_infoKey  = "message"
)

var manager *Manager

// A Manager represents a manager server
type Manager struct {
	cfg        *Config
	engine     *gin.Engine
	adapter    dbAdapter
	rwmu       sync.RWMutex
	httpClient *http.Client
}

// GetTUNASyncManager returns the manager from config
func GetTUNASyncManager(cfg *Config) *Manager {
	if manager != nil {
		return manager
	}

	// create gin engine
	if !cfg.Debug {
		gin.SetMode(gin.ReleaseMode)
	}
	s := &Manager{
		cfg:     cfg,
		adapter: nil,
	}

	s.engine = gin.New()
	s.engine.Use(gin.Recovery())
	if cfg.Debug {
		s.engine.Use(gin.Logger())
	}

	if cfg.Files.CACert != "" {
		httpClient, err := CreateHTTPClient(cfg.Files.CACert)
		if err != nil {
			logger.Errorf("初始化 HTTP 客户端时出错: %s", err.Error())
			return nil
		}
		s.httpClient = httpClient
	}

	if cfg.Files.DBFile != "" {
		adapter, err := makeDBAdapter(cfg.Files.DBType, cfg.Files.DBFile)
		if err != nil {
			logger.Errorf("初始化 DBtype 时出错: %s", err.Error())
			return nil
		}
		s.setDBAdapter(adapter)
	}

	// common log middleware
	s.engine.Use(contextErrorLogger)

	s.engine.GET("/ping", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{_infoKey: "pong"})
	})
	// list jobs, status page
	s.engine.GET("/jobs", s.listAllJobs)
	// flush disabled jobs
	s.engine.DELETE("/jobs/disabled", s.flushDisabledJobs)

	// list workers
	s.engine.GET("/workers", s.listWorkers)
	// worker online
	s.engine.POST("/workers", s.registerWorker)

	// workerID should be valid in this route group
	workerValidateGroup := s.engine.Group("/workers", s.workerIDValidator)
	{
		// delete specified worker
		workerValidateGroup.DELETE(":id", s.deleteWorker)
		// get job list
		workerValidateGroup.GET(":id/jobs", s.listJobsOfWorker)
		// post job status
		workerValidateGroup.POST(":id/jobs/:job", s.updateJobOfWorker)
		workerValidateGroup.POST(":id/jobs/:job/size", s.updateMirrorSize)
		workerValidateGroup.POST(":id/schedules", s.updateSchedulesOfWorker)
	}

	// for tunasynctl to post commands
	s.engine.POST("/cmd", s.handleClientCmd)

	manager = s
	return s
}

func (s *Manager) setDBAdapter(adapter dbAdapter) {
	s.adapter = adapter
}

// Run runs the manager server forever
func (s *Manager) Run() {
	addr := fmt.Sprintf("%s:%d", s.cfg.Server.Addr, s.cfg.Server.Port)

	httpServer := &http.Server{
		Addr:         addr,
		Handler:      s.engine,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
	}

	if s.cfg.Server.SSLCert == "" && s.cfg.Server.SSLKey == "" {
		if err := httpServer.ListenAndServe(); err != nil {
			panic(err)
		}
	} else {
		if err := httpServer.ListenAndServeTLS(s.cfg.Server.SSLCert, s.cfg.Server.SSLKey); err != nil {
			panic(err)
		}
	}
}

// listAllJobs respond with all jobs of specified workers
func (s *Manager) listAllJobs(c *gin.Context) {
	s.rwmu.RLock()
	mirrorStatusList, err := s.adapter.ListAllMirrorStatus()
	s.rwmu.RUnlock()
	if err != nil {
		err := fmt.Errorf("未能列出所有镜像状态: %s",
			err.Error(),
		)
		c.Error(err)
		s.returnErrJSON(c, http.StatusInternalServerError, err)
		return
	}
	webMirStatusList := []WebMirrorStatus{}
	for _, m := range mirrorStatusList {
		webMirStatusList = append(
			webMirStatusList,
			BuildWebMirrorStatus(m),
		)
	}
	c.JSON(http.StatusOK, webMirStatusList)
}

// flushDisabledJobs deletes all jobs that marks as deleted
func (s *Manager) flushDisabledJobs(c *gin.Context) {
	s.rwmu.Lock()
	err := s.adapter.FlushDisabledJobs()
	s.rwmu.Unlock()
	if err != nil {
		err := fmt.Errorf("未能刷新禁用的作业: %s",
			err.Error(),
		)
		c.Error(err)
		s.returnErrJSON(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{_infoKey: "flushed"})
}

// deleteWorker deletes one worker by id
func (s *Manager) deleteWorker(c *gin.Context) {
	workerID := c.Param("id")
	s.rwmu.Lock()
	err := s.adapter.DeleteWorker(workerID)
	s.rwmu.Unlock()
	if err != nil {
		err := fmt.Errorf("未能删除工作流: %s",
			err.Error(),
		)
		c.Error(err)
		s.returnErrJSON(c, http.StatusInternalServerError, err)
		return
	}
	logger.Noticef("Worker <%s> deleted", workerID)
	c.JSON(http.StatusOK, gin.H{_infoKey: "deleted"})
}

// listWorkers respond with information of all the workers
func (s *Manager) listWorkers(c *gin.Context) {
	var workerInfos []WorkerStatus
	s.rwmu.RLock()
	workers, err := s.adapter.ListWorkers()
	s.rwmu.RUnlock()
	if err != nil {
		err := fmt.Errorf("未能列出工作流: %s",
			err.Error(),
		)
		c.Error(err)
		s.returnErrJSON(c, http.StatusInternalServerError, err)
		return
	}
	for _, w := range workers {
		workerInfos = append(workerInfos,
			WorkerStatus{
				ID:           w.ID,
				URL:          w.URL,
				Token:        "REDACTED",
				LastOnline:   w.LastOnline,
				LastRegister: w.LastRegister,
			})
	}
	c.JSON(http.StatusOK, workerInfos)
}

// registerWorker register an newly-online worker
func (s *Manager) registerWorker(c *gin.Context) {
	var _worker WorkerStatus
	c.BindJSON(&_worker)
	_worker.LastOnline = time.Now()
	_worker.LastRegister = time.Now()
	newWorker, err := s.adapter.CreateWorker(_worker)
	if err != nil {
		err := fmt.Errorf("未能注册工作流: %s",
			err.Error(),
		)
		c.Error(err)
		s.returnErrJSON(c, http.StatusInternalServerError, err)
		return
	}

	logger.Noticef("Worker <%s> registered", _worker.ID)
	// create workerCmd channel for this worker
	c.JSON(http.StatusOK, newWorker)
}

// listJobsOfWorker respond with all the jobs of the specified worker
func (s *Manager) listJobsOfWorker(c *gin.Context) {
	workerID := c.Param("id")
	s.rwmu.RLock()
	mirrorStatusList, err := s.adapter.ListMirrorStatus(workerID)
	s.rwmu.RUnlock()
	if err != nil {
		err := fmt.Errorf("未能列出工人的工作 %s: %s",
			workerID, err.Error(),
		)
		c.Error(err)
		s.returnErrJSON(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, mirrorStatusList)
}

func (s *Manager) returnErrJSON(c *gin.Context, code int, err error) {
	c.JSON(code, gin.H{
		_errorKey: err.Error(),
	})
}

func (s *Manager) updateSchedulesOfWorker(c *gin.Context) {
	workerID := c.Param("id")
	var schedules MirrorSchedules
	c.BindJSON(&schedules)

	for _, schedule := range schedules.Schedules {
		mirrorName := schedule.MirrorName
		if len(mirrorName) == 0 {
			s.returnErrJSON(
				c, http.StatusBadRequest,
				errors.New("镜像名称不能为空"),
			)
		}

		s.rwmu.RLock()
		s.adapter.RefreshWorker(workerID)
		curStatus, err := s.adapter.GetMirrorStatus(workerID, mirrorName)
		s.rwmu.RUnlock()
		if err != nil {
			logger.Errorf("failed to get job %s of worker %s: %s",
				mirrorName, workerID, err.Error(),
			)
			continue
		}

		if curStatus.Scheduled == schedule.NextSchedule {
			// no changes, skip update
			continue
		}

		curStatus.Scheduled = schedule.NextSchedule
		s.rwmu.Lock()
		_, err = s.adapter.UpdateMirrorStatus(workerID, mirrorName, curStatus)
		s.rwmu.Unlock()
		if err != nil {
			err := fmt.Errorf("failed to update job %s of worker %s: %s",
				mirrorName, workerID, err.Error(),
			)
			c.Error(err)
			s.returnErrJSON(c, http.StatusInternalServerError, err)
			return
		}
	}
	type empty struct{}
	c.JSON(http.StatusOK, empty{})
}

func (s *Manager) updateJobOfWorker(c *gin.Context) {
	workerID := c.Param("id")
	var status MirrorStatus
	c.BindJSON(&status)
	mirrorName := status.Name
	if len(mirrorName) == 0 {
		s.returnErrJSON(
			c, http.StatusBadRequest,
			errors.New("镜像名称不能为空"),
		)
	}

	s.rwmu.RLock()
	s.adapter.RefreshWorker(workerID)
	curStatus, _ := s.adapter.GetMirrorStatus(workerID, mirrorName)
	s.rwmu.RUnlock()

	curTime := time.Now()

	if status.Status == PreSyncing && curStatus.Status != PreSyncing {
		status.LastStarted = curTime
	} else {
		status.LastStarted = curStatus.LastStarted
	}
	// Only successful syncing needs last_update
	if status.Status == Success {
		status.LastUpdate = curTime
	} else {
		status.LastUpdate = curStatus.LastUpdate
	}
	if status.Status == Success || status.Status == Failed {
		status.LastEnded = curTime
	} else {
		status.LastEnded = curStatus.LastEnded
	}

	// Only message with meaningful size updates the mirror size
	if len(curStatus.Size) > 0 && curStatus.Size != "unknown" {
		if len(status.Size) == 0 || status.Size == "unknown" {
			status.Size = curStatus.Size
		}
	}

	// for logging
	switch status.Status {
	case Syncing:
		logger.Noticef("Job [%s] @<%s> starts syncing", status.Name, status.Worker)
	default:
		logger.Noticef("Job [%s] @<%s> %s", status.Name, status.Worker, status.Status)
	}

	s.rwmu.Lock()
	newStatus, err := s.adapter.UpdateMirrorStatus(workerID, mirrorName, status)
	s.rwmu.Unlock()
	if err != nil {
		err := fmt.Errorf("failed to update job %s of worker %s: %s",
			mirrorName, workerID, err.Error(),
		)
		c.Error(err)
		s.returnErrJSON(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, newStatus)
}

func (s *Manager) updateMirrorSize(c *gin.Context) {
	workerID := c.Param("id")
	type SizeMsg struct {
		Name string `json:"name"`
		Size string `json:"size"`
	}
	var msg SizeMsg
	c.BindJSON(&msg)

	mirrorName := msg.Name
	s.rwmu.RLock()
	s.adapter.RefreshWorker(workerID)
	status, err := s.adapter.GetMirrorStatus(workerID, mirrorName)
	s.rwmu.RUnlock()
	if err != nil {
		logger.Errorf(
			"Failed to get status of mirror %s @<%s>: %s",
			mirrorName, workerID, err.Error(),
		)
		s.returnErrJSON(c, http.StatusInternalServerError, err)
		return
	}

	// Only message with meaningful size updates the mirror size
	if len(msg.Size) > 0 || msg.Size != "unknown" {
		status.Size = msg.Size
	}

	logger.Noticef("Mirror size of [%s] @<%s>: %s", status.Name, status.Worker, status.Size)

	s.rwmu.Lock()
	newStatus, err := s.adapter.UpdateMirrorStatus(workerID, mirrorName, status)
	s.rwmu.Unlock()
	if err != nil {
		err := fmt.Errorf("failed to update job %s of worker %s: %s",
			mirrorName, workerID, err.Error(),
		)
		c.Error(err)
		s.returnErrJSON(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, newStatus)
}

func (s *Manager) handleClientCmd(c *gin.Context) {
	var clientCmd ClientCmd
	c.BindJSON(&clientCmd)
	workerID := clientCmd.WorkerID
	if workerID == "" {
		// TODO: decide which worker should do this mirror when WorkerID is null string
		logger.Errorf("handleClientCmd case workerID == \" \" not implemented yet")
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}

	s.rwmu.RLock()
	w, err := s.adapter.GetWorker(workerID)
	s.rwmu.RUnlock()
	if err != nil {
		err := fmt.Errorf("worker %s is not registered yet", workerID)
		s.returnErrJSON(c, http.StatusBadRequest, err)
		return
	}
	workerURL := w.URL
	// parse client cmd into worker cmd
	workerCmd := WorkerCmd{
		Cmd:      clientCmd.Cmd,
		MirrorID: clientCmd.MirrorID,
		Args:     clientCmd.Args,
		Options:  clientCmd.Options,
	}

	// update job status, even if the job did not disable successfully,
	// this status should be set as disabled
	s.rwmu.RLock()
	curStat, _ := s.adapter.GetMirrorStatus(clientCmd.WorkerID, clientCmd.MirrorID)
	s.rwmu.RUnlock()
	changed := false
	switch clientCmd.Cmd {
	case CmdDisable:
		curStat.Status = Disabled
		changed = true
	case CmdStop:
		curStat.Status = Paused
		changed = true
	}
	if changed {
		s.rwmu.Lock()
		s.adapter.UpdateMirrorStatus(clientCmd.WorkerID, clientCmd.MirrorID, curStat)
		s.rwmu.Unlock()
	}

	logger.Noticef("Posting command '%s %s' to <%s>", clientCmd.Cmd, clientCmd.MirrorID, clientCmd.WorkerID)
	// post command to worker
	_, err = PostJSON(workerURL, workerCmd, s.httpClient)
	if err != nil {
		err := fmt.Errorf("post command to worker %s(%s) fail: %s", workerID, workerURL, err.Error())
		c.Error(err)
		s.returnErrJSON(c, http.StatusInternalServerError, err)
		return
	}
	// TODO: check response for success
	c.JSON(http.StatusOK, gin.H{_infoKey: "成功向工作流发送命令 " + workerID})
}
