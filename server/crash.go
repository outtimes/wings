package server

import (
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/pterodactyl/wings/config"
	"github.com/pterodactyl/wings/environment"
)

type CrashHandler struct {
	mu sync.RWMutex

	// Tracks the time of the last server crash event.
	lastCrash time.Time
}

// Returns the time of the last crash for this server instance.
func (cd *CrashHandler) LastCrashTime() time.Time {
	cd.mu.RLock()
	defer cd.mu.RUnlock()

	return cd.lastCrash
}

// Sets the last crash time for a server.
func (cd *CrashHandler) SetLastCrash(t time.Time) {
	cd.mu.Lock()
	cd.lastCrash = t
	cd.mu.Unlock()
}

// Looks at the environment exit state to determine if the process exited cleanly or
// if it was the result of an event that we should try to recover from.
//
// This function assumes it is called under circumstances where a crash is suspected
// of occurring. It will not do anything to determine if it was actually a crash, just
// look at the exit state and check if it meets the criteria of being called a crash
// by Wings.
//
// If the server is determined to have crashed, the process will be restarted and the
// counter for the server will be incremented.
func (s *Server) handleServerCrash() error {
	// No point in doing anything here if the server isn't currently offline, there
	// is no reason to do a crash detection event. If the server crash detection is
	// disabled we want to skip anything after this as well.
	if s.Environment.State() != environment.ProcessOfflineState || !s.Config().CrashDetectionEnabled {
		if !s.Config().CrashDetectionEnabled {
			s.Log().Debug("server triggered crash detection but handler is disabled for server process")
			s.PublishConsoleOutputFromDaemon("因此服务器的崩溃检测已被停用，正在停止自动启动")
		}

		return nil
	}

	exitCode, oomKilled, err := s.Environment.ExitState()
	if err != nil {
		return err
	}

	// If the system is not configured to detect a clean exit code as a crash, and the
	// crash is not the result of the program running out of memory, do nothing.
	if exitCode == 0 && !oomKilled && !config.Get().System.CrashDetection.DetectCleanExitAsCrash {
		s.Log().Debug("server exited with successful exit code; system is configured to not detect this as a crash")
		return nil
	}

	s.PublishConsoleOutputFromDaemon("---------- 检测到服务器崩溃 ----------")
	s.PublishConsoleOutputFromDaemon(fmt.Sprintf("推出状态码: %d", exitCode))
	s.PublishConsoleOutputFromDaemon(fmt.Sprintf("是否为内存溢出: %t", oomKilled))

	c := s.crasher.LastCrashTime()
	timeout := config.Get().System.CrashDetection.Timeout

	// If the last crash time was within the last `timeout` seconds we do not want to perform
	// an automatic reboot of the process. Return an error that can be handled.
	//
	// If timeout is set to 0, always reboot the server (this is probably a terrible idea, but some people want it)
	if timeout != 0 && !c.IsZero() && c.Add(time.Second*time.Duration(config.Get().System.CrashDetection.Timeout)).After(time.Now()) {
		s.PublishConsoleOutputFromDaemon("已停止自动重启, 因为上一次崩溃的发生是在 " + strconv.Itoa(timeout) + " 秒以内。")
		return &crashTooFrequent{}
	}

	s.crasher.SetLastCrash(time.Now())

	return s.HandlePowerAction(PowerActionStart)
}
