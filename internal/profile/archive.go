package profile

import (
	"archive/zip"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"time"
)

const (
	defaultMaxFileNum = 100
	defaultMaxHistory = time.Hour * 24
	defaultTimeFormat = "2006-01-02T15:04:05.000Z07:00"
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
	zipFilePath := filepath.Join(m.archiveDir, time.Now().Format(defaultTimeFormat)+".zip")
	zipFile, err := os.Create(zipFilePath)
	if err != nil {
		m.errorLog("create archive file failed", err)
		return
	}
	defer zipFile.Close()
	zipWriter := zip.NewWriter(zipFile)
	defer zipWriter.Close()
	for _, f := range collection {
		info, err := os.Stat(f)
		if err != nil {
			m.errorLog(fmt.Sprintf("read status of file %q failed", f), err)
			continue
		}
		fileHeader, err := zip.FileInfoHeader(info)
		if err != nil {
			m.errorLog(fmt.Sprintf("get fileHeader of %q failed", f), err)
			continue
		}
		writer, err := zipWriter.CreateHeader(fileHeader)
		if err != nil {
			m.errorLog(fmt.Sprintf("write fileHeader of %q failed", f), err)
			continue
		}
		data, err := ioutil.ReadFile(f)
		if err != nil {
			m.errorLog(fmt.Sprintf("read profile %q failed", f), err)
			data = []byte{0}
		}
		_, err = writer.Write(data)
		if err != nil {
			m.errorLog(fmt.Sprintf("write zip of file %q failed", f), err)
			return
		}
	}
}
