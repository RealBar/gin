package profile

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime/pprof"
	"runtime/trace"
	"strings"
	"sync"
	"time"
)

const (
	Cpu          Profile = "cpu"
	Heap         Profile = "heap"
	ThreadCreate Profile = "threadcreate"
	Goroutine    Profile = "goroutine"
	Block        Profile = "block"
	Mutex        Profile = "mutex"
	Trace        Profile = "trace"
)

var profileCollection = map[Profile]struct{}{Cpu: {}, Heap: {}, ThreadCreate: {}, Goroutine: {},
	Block: {}, Mutex: {}, Trace: {}}
var profileOnceLock sync.Once
var defaultFormat = &Format{
	FileNameFormat: "{type}_{timestamp}.profile",
	TimeFormat:     defaultTimeFormat,
}
var manager *profileManager

type Format struct {
	TimeFormat     string
	FileNameFormat string // et :"{type}_{timestamp}.profile"
	formatFunc     func(string, Profile) string
	lock           sync.Mutex
}

func (f *Format) format(time1 time.Time, type1 Profile) string {
	f.lock.Lock()
	defer f.lock.Unlock()
	if f.formatFunc == nil {
		tmp := strings.Replace(f.FileNameFormat, "{type}", "%s", 1)
		tmp = strings.Replace(tmp, "{timestamp}", "%s", 1)
		typeIdx := strings.Index(f.FileNameFormat, "{type}")
		timestampIdx := strings.Index(f.FileNameFormat, "{timestamp}")
		if typeIdx < timestampIdx {
			f.formatFunc = func(time2 string, type2 Profile) string {
				return fmt.Sprintf(tmp, type2, time2)
			}
		} else {
			f.formatFunc = func(time2 string, type2 Profile) string {
				return fmt.Sprintf(tmp, time2, type2)
			}
		}
	}
	return f.formatFunc(time1.Format(f.TimeFormat), type1)
}

type profileManager struct {
	*Option
	ticker         *time.Ticker
	fileCollection []string
	archiveDir     string
	err            error
	lock           sync.Mutex
}

type Option struct {
	Y             time.Duration // do profiling for X for every Y,
	X             time.Duration
	StoreDir      string  // place to store the profiles
	Compress      bool    // whether to compress the profiles.By default the profiles are compressed daily by gzip.
	FileFormat    *Format // profile file name format, if not set, defaultFormat will be used
	LogOutput     io.Writer
	ErrLogOutput  io.Writer
	ArchivePolicy ArchivePolicy
}

type Profile string

func EnableProfile(opt *Option, profiles ...Profile) error {
	if manager != nil {
		return errors.New("cannot call EnableProfile repeatedly")
	}
	err := checkOpt(*opt, profiles)
	if err != nil {
		return err
	}
	profileOnceLock.Do(func() {
		manager = &profileManager{
			Option: opt,
		}
		manager.ticker = time.NewTicker(opt.Y)
		if manager.Compress {
			manager.archiveDir = filepath.Join(manager.StoreDir, "archive")
			manager.err = createDirIfNotExists(manager.archiveDir)
			if manager.FileFormat == nil {
				manager.FileFormat = defaultFormat
			}
			if manager.ArchivePolicy == nil {
				manager.ArchivePolicy = &FileNumArchivePolicy{}
			}
		}
	})
	if manager.err != nil {
		return err
	}
	go manager.doProfile(profiles...)
	return nil
}

func checkOpt(opt Option, profiles []Profile) error {
	if opt.Y <= 0 || opt.X <= 0 {
		return errors.New("Y or X should not <= 0")
	}
	if opt.Y <= opt.X {
		return errors.New("Y should not <= X")
	}
	if opt.Y <= 1*time.Second {
		return errors.New("too frequent profile may impact the performance, Y is suggested to be > 1s")
	}

	for _, p := range profiles {
		if _, ok := profileCollection[p]; !ok {
			return errors.New(fmt.Sprintf("profile %q not valid", p))
		}
	}
	if len(profiles) == 0 {
		return errors.New("no profile set")
	}

	return createDirIfNotExists(opt.StoreDir)
}

func (m *profileManager) doProfile(profiles ...Profile) {
	for {
		<-m.ticker.C
		for _, p := range profiles {
			switch p {
			case Cpu, Trace:
				go m.doDurationProfile(p)
			case Heap, ThreadCreate, Goroutine, Block, Mutex:
				go m.doInstantProfile(p)
			}
		}
		m.checkArchive()
	}
}

func (m *profileManager) doDurationProfile(profile Profile) {
	filePath := getFilePath(profile, m.StoreDir, m.FileFormat)
	file, err := m.openFile(filePath)
	if err != nil {
		m.errorLog(fmt.Sprintf("create profile %q failed", filePath), err)
		return
	}
	defer m.closeFile(file, filePath)
	switch profile {
	case Cpu:
		err = pprof.StartCPUProfile(file)
		if err != nil {
			m.errorLog("StartCPUProfile failed", err)
			return
		}
		m.infoLog("StartCPUProfile succeed")
		defer pprof.StopCPUProfile()
	case Trace:
		err = trace.Start(m.ErrLogOutput)
		if err != nil {
			m.errorLog("trace start failed", err)
			return
		}
		m.infoLog("trace.Start succeed")
		defer trace.Stop()
	}
	time.Sleep(m.X)
}

func (m *profileManager) doInstantProfile(profile Profile) {
	filePath := getFilePath(profile, m.StoreDir, m.FileFormat)
	file, err := m.openFile(filePath)
	if err != nil {
		m.errorLog("open file failed", err)
		return
	}
	defer m.closeFile(file, filePath)
	p := pprof.Lookup(string(profile))
	err = p.WriteTo(file, 0)
	if err != nil {
		m.errorLog("write profile failed", err)
		return
	}
	m.infoLog(fmt.Sprintf("%s profile finished", string(profile)))
}

func (m *profileManager) getFileCollection() []string {
	m.lock.Lock()
	defer m.lock.Unlock()
	return m.fileCollection
}
func (m *profileManager) closeFile(file *os.File, filePath string) {
	m.lock.Lock()
	defer m.lock.Unlock()
	if err := file.Close(); err != nil {
		m.errorLog(fmt.Sprintf("close profile %q failed", filePath), err)
		return
	}
	m.fileCollection = append(m.fileCollection, filePath)
}

func (m *profileManager) removeCollection(oldColl []string) {
	m.lock.Lock()
	defer m.lock.Unlock()
	currLen := len(m.fileCollection)
	oldLen := len(oldColl)
	if currLen == oldLen {
		m.fileCollection = m.fileCollection[:0]
	} else if currLen > oldLen {
		copy(m.fileCollection, m.fileCollection[oldLen:])
		m.fileCollection = m.fileCollection[:currLen-oldLen]
	}
}
func (m *profileManager) removeFiles(c []string) {
	for _, f := range c {
		err := os.Remove(f)
		if err != nil {
			// if first remove failed, perhaps it is because the writing goroutine has not close it yet.
			// Wait 10ms before try again
			time.Sleep(100 * time.Millisecond)
			err = os.Remove(f)
			if err != nil {
				m.errorLog("remove profile failed", err)
			}
		}
	}
}
func (m *profileManager) openFile(filePath string) (*os.File, error) {
	return os.OpenFile(filePath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0644)
}

func getFilePath(profile Profile, dir string, f *Format) string {
	fileName := f.format(time.Now(), profile)
	return filepath.Join(dir, fileName)
}

func (m *profileManager) errorLog(msg string, err error) {
	_, _ = fmt.Fprintf(m.ErrLogOutput, "[GIN][ERROR] %v |%s|error:%s\n",
		time.Now().Format("2006/01/02 - 15:04:05"), msg, err.Error())
}

func (m *profileManager) infoLog(msg string) {
	_, _ = fmt.Fprintf(m.LogOutput, "[GIN][INFO] %v |%s\n",
		time.Now().Format("2006/01/02 - 15:04:05"), msg)
}

func createDirIfNotExists(dir string) error {
	info, err := os.Stat(dir)
	if err != nil {
		if os.IsNotExist(err) {
			err := os.MkdirAll(dir, 0755)
			if err != nil {
				return err
			}
		} else {
			return err
		}
	} else if !info.IsDir() {
		return errors.New(fmt.Sprintf("%q is not directory", dir))
	}
	return nil
}

func (m *profileManager) checkArchive() {
	collection := m.getFileCollection()
	if m.ArchivePolicy.needArchive(collection) {
		m.infoLog(fmt.Sprintf("start to archive files:%v", collection))
		m.doArchive0(collection)
		m.removeCollection(collection)
		m.removeFiles(collection)
	}
}
