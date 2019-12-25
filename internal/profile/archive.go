package profile

import (
	"archive/tar"
	"fmt"
	"io/ioutil"
	"os"
	"path"
	"time"
)

const (
	defaultMaxFileNum = 100
	defaultMaxHistory = time.Hour * 24
)

type ArchivePolicy interface {
	needArchive(fileCollection []string) bool
}

type FileNumArchivePolicy struct {
	MaxFileNum int
}

func (f *FileNumArchivePolicy) needArchive(fileCollection []string) bool {
	if f.MaxFileNum == 0 {
		f.MaxFileNum = defaultMaxFileNum
	}
	return len(fileCollection) >= f.MaxFileNum
}

type TimeArchivePolicy struct {
	MaxHistory      time.Duration
	lastArchiveTime time.Time
}

func (f *TimeArchivePolicy) needArchive(fileCollection []string) bool {
	if f.MaxHistory == 0 {
		f.MaxHistory = defaultMaxHistory
	}
	if f.lastArchiveTime.IsZero() {
		f.lastArchiveTime = time.Now()
		return true
	}
	return time.Since(f.lastArchiveTime) >= f.MaxHistory
}

func (m *profileManager) doArchive0(collection []string) {
	filePath := path.Join(m.archiveDir, time.Now().Format(time.RFC3339)+".tar")
	file, err := os.Create(filePath)
	if err != nil {
		m.errorLog("create tar file failed", err)
		return
	}
	defer file.Close()
	tarWriter := tar.NewWriter(file)
	defer tarWriter.Close()
	for _, f := range collection {
		info, err := os.Stat(f)
		if err != nil {
			m.errorLog(fmt.Sprintf("read status of file %q failed", f), err)
			continue
		}
		header, err := tar.FileInfoHeader(info, "")
		if err != nil {
			m.errorLog(fmt.Sprintf("read info header of file %q failed", f), err)
			continue
		}
		err = tarWriter.WriteHeader(header)
		if err != nil {
			m.errorLog(fmt.Sprintf("write tar header info header of file %q failed", f), err)
			return
		}
		data, err := ioutil.ReadFile(f)
		if err != nil {
			m.errorLog(fmt.Sprintf("read tar input file %q failed", f), err)
			continue
		}
		_, err = tarWriter.Write(data)
		if err != nil {
			m.errorLog(fmt.Sprintf("write tar of file %q failed", f), err)
			return
		}
	}
}
