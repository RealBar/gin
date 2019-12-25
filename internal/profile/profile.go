package profile

import (
	"archive/tar"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"path"
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
	TimeFormat:     time.StampMilli,
}
var manager *profileManager

type Format struct {
	TimeFormat     string
	FileNameFormat string // et :"{type}_{timestamp}.profile"
	formatFunc     func(time.Time, Profile) string
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
			f.formatFunc = func(time2 time.Time, type2 Profile) string {
				return fmt.Sprintf(tmp, type2, time2)
			}
		} else {
			f.formatFunc = func(time2 time.Time, type2 Profile) string {
				return fmt.Sprintf(tmp, time2, type2)
			}
		}
	}
	return f.formatFunc(time1, type1)
}

type profileManager struct {
	Option
	ticker          *time.Ticker
	lastCompressDay int
	fileCollection  []string
	archiveDir      string
	err             error
	lock            sync.Mutex
}

type Option struct {
	TotalDuration   time.Duration // do profiling for TotalDuration for every ProfileDuration,
	ProfileDuration time.Duration
	StoreDir        string  // place to store the profiles
	Compress        bool    // whether to compress the profiles.By default the profiles are compressed daily by gzip.
	FileFormat      *Format // profile file name format, if not set, defaultFormat will be used
	LogOutput       io.Writer
	ErrLogOutput    io.Writer
	MaxHistory      int // num of days to save profiles
	MaxFileNum      int // num of files
}

type Profile string

func EnableProfile(opt Option, profiles ...Profile) error {
	if manager != nil {
		return errors.New("cannot call EnableProfile repeatedly")
	}
	err := checkOpt(opt, profiles)
	if err != nil {
		return err
	}
	profileOnceLock.Do(func() {
		manager := &profileManager{
			Option: opt,
		}
		if manager.FileFormat == nil {
			manager.FileFormat = defaultFormat
		}
		manager.ticker = time.NewTicker(opt.TotalDuration)
		if manager.Compress {
			manager.lastCompressDay = time.Now().Day()
			manager.archiveDir = path.Join(manager.StoreDir, "archive")
			manager.err = createDirIfNotExists(manager.archiveDir)
		}
	})
	if manager.err != nil {
		return err
	}
	go manager.doProfile()
	return nil
}

func checkOpt(opt Option, profiles []Profile) error {
	if opt.TotalDuration <= 0 || opt.ProfileDuration <= 0 {
		return errors.New("TotalDuration or ProfileDuration should not <= 0")
	}
	if opt.TotalDuration <= opt.ProfileDuration {
		return errors.New("TotalDuration should not <= ProfileDuration")
	}
	if opt.TotalDuration <= 1*time.Second {
		return errors.New("too frequent profile may impact the performance, TotalDuration is suggested to be > 1s")
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
		if m.Compress && time.Now().Day() > m.getLastCompressDay() {
			go m.doCompress(m.StoreDir, m.archiveDir, m.ErrLogOutput)
		}
		for _, p := range profiles {
			switch p {
			case Cpu, Trace:
				go m.doDurationProfile(p)
			case Heap, ThreadCreate, Goroutine, Block, Mutex:
				go m.doInstantProfile(p)
			}
		}
	}
}

func (m *profileManager) doDurationProfile(profile Profile) {
	filePath := getFilePath(profile, m.StoreDir, m.FileFormat)
	file, err := openFile(filePath)
	if err != nil {
		errorLog(m.ErrLogOutput, "open file failed", err)
		return
	}
	defer m.addFileCollection(filePath)
	defer file.Close()
	switch profile {
	case Cpu:
		err = pprof.StartCPUProfile(file)
		if err != nil {
			errorLog(m.ErrLogOutput, "StartCPUProfile failed", err)
			return
		}
		defer pprof.StopCPUProfile()
	case Trace:
		err = trace.Start(m.ErrLogOutput)
		if err != nil {
			errorLog(m.ErrLogOutput, "trace start failed", err)
			return
		}
		defer trace.Stop()
	}
	time.Sleep(m.ProfileDuration)
}

func (m *profileManager) doInstantProfile(profile Profile) {
	filePath := getFilePath(profile, m.StoreDir, m.FileFormat)
	file, err := openFile(filePath)
	if err != nil {
		errorLog(m.ErrLogOutput, "open file failed", err)
		return
	}
	defer file.Close()
	p := pprof.Lookup(string(profile))
	err = p.WriteTo(file, 0)
	if err != nil {
		errorLog(m.ErrLogOutput, "write profile failed", err)
		return
	}
	m.addFileCollection(filePath)
}
func (m *profileManager) addFileCollection(filePath string) {
	m.lock.Lock()
	defer m.lock.Unlock()
	m.fileCollection = append(m.fileCollection, filePath)
}
func (m *profileManager) getFileCollection() []string {
	m.lock.Lock()
	defer m.lock.Unlock()
	return m.fileCollection
}
func (m *profileManager) getLastCompressDay() int {
	m.lock.Lock()
	defer m.lock.Unlock()
	return m.lastCompressDay
}
func (m *profileManager) setLastCompressDay(day int) {
	m.lock.Lock()
	defer m.lock.Unlock()
	m.lastCompressDay = time.Now().Day()
}
func (m *profileManager) removeCollection(oldColl []string) {
	m.lock.Lock()
	defer m.lock.Unlock()
	currLen := len(m.fileCollection)
	oldLen := len(oldColl)

	if currLen == oldLen {
		m.fileCollection = m.fileCollection[:0]
	} else {
		copy(m.fileCollection, m.fileCollection[oldLen:])
		m.fileCollection = m.fileCollection[:currLen-oldLen]
	}
}
func openFile(filePath string) (*os.File, error) {
	return os.OpenFile(filePath, os.O_CREATE|os.O_EXCL, 0644)
}

func getFilePath(profile Profile, dir string, f *Format) string {
	fileName := f.format(time.Now(), profile)
	return path.Join(dir, fileName)
}

func errorLog(w io.Writer, msg string, err error) {
	_, _ = fmt.Fprintf(w, "[GIN] %v |%s|error:%s|",
		time.Now().Format("2006/01/02 - 15:04:05"), msg, err.Error())
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

func (m *profileManager) doCompress(readDir, storeDir string, w io.Writer) {
	collection := m.getFileCollection()
	if len(collection) == 0 {
		return
	}
	defer m.removeCollection(collection)
	filePath := path.Join(storeDir, time.Now().Format(time.RFC3339)+".tar")
	file, err := os.Create(filePath)
	if err != nil {
		errorLog(w, "create tar file failed", err)
		return
	}
	defer file.Close()
	tarWriter := tar.NewWriter(file)
	defer tarWriter.Close()
	defer m.setLastCompressDay(time.Now().Day())
	for _, f := range collection {
		info, err := os.Stat(f)
		if err != nil {
			errorLog(w, fmt.Sprintf("read status of file %q failed", f), err)
			continue
		}
		header, err := tar.FileInfoHeader(info, "")
		if err != nil {
			errorLog(w, fmt.Sprintf("read info header of file %q failed", f), err)
			continue
		}
		err = tarWriter.WriteHeader(header)
		if err != nil {
			errorLog(w, fmt.Sprintf("write tar header info header of file %q failed", f), err)
			return
		}
		data, err := ioutil.ReadFile(f)
		if err != nil {
			errorLog(w, fmt.Sprintf("read tar input file %q failed", f), err)
			continue
		}
		_, err = tarWriter.Write(data)
		if err != nil {
			errorLog(w, fmt.Sprintf("write tar of file %q failed", f), err)
			return
		}
	}
}
