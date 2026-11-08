package service

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
	"virdeck/core/adb"
)

const DefaultTransferDirectory = "/sdcard/Download/VirDeck"

type SendFilesRequest struct {
	IDs        []string
	LocalPaths []string
	RemoteDir  string
}
type TransferFile struct {
	Name  string
	IsDir bool
}
type fileADB interface {
	TransferSend(context.Context, int, []string, string) (string, error)
	TransferReceive(context.Context, int, string, string) error
	TransferList(context.Context, int, string) ([]adb.TransferEntry, error)
}

func (m *Manager) SendFiles(ctx context.Context, req SendFilesRequest) error {
	m.maintenance.RLock()
	defer m.maintenance.RUnlock()
	if req.RemoteDir == "" {
		req.RemoteDir = DefaultTransferDirectory
	}
	dir, err := adb.SharedStoragePath(req.RemoteDir)
	if err != nil {
		return err
	}
	if len(req.LocalPaths) == 0 {
		return errors.New("请选择要发送的本地文件")
	}
	for _, p := range req.LocalPaths {
		info, e := os.Stat(p)
		if e != nil {
			return e
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("只支持普通文件: %s", p)
		}
	}
	ports, err := m.resolveRunningPorts(req.IDs)
	if err != nil {
		return err
	}
	backend, ok := m.adb.(fileADB)
	if !ok {
		return errors.New("当前 ADB 后端不支持文件传输")
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	var errs []error
	for id, port := range ports {
		dest, e := backend.TransferSend(ctx, port, req.LocalPaths, dir)
		if e != nil {
			e = fmt.Errorf("[%s] 发送文件失败（目录 %s）: %w", id, dest, e)
			m.emitLog(id, e.Error())
			errs = append(errs, e)
		} else {
			m.emitLog(id, fmt.Sprintf("已发送 %d 个文件到 %s", len(req.LocalPaths), dest))
		}
	}
	return errors.Join(errs...)
}

func (m *Manager) ReceiveFiles(ctx context.Context, id, remotePath, localDir string) (string, error) {
	m.maintenance.RLock()
	defer m.maintenance.RUnlock()
	remotePath, err := adb.SharedStoragePath(remotePath)
	if err != nil {
		return "", err
	}
	ports, err := m.resolveRunningPorts([]string{id})
	if err != nil {
		return "", err
	}
	backend, ok := m.adb.(fileADB)
	if !ok {
		return "", errors.New("当前 ADB 后端不支持文件传输")
	}
	if localDir == "" {
		return "", errors.New("请选择 Windows 接收目录")
	}
	localDir, err = filepath.Abs(localDir)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(localDir)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", errors.New("接收位置必须是目录")
	}
	export, err := os.MkdirTemp(localDir, "VirDeck-export-")
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	if err = backend.TransferReceive(ctx, ports[id], remotePath, export); err != nil {
		// Keep partial results in the reserved folder and include its location.
		err = fmt.Errorf("接收 %s 失败（导出目录 %s）: %w", remotePath, export, err)
		m.emitLog(id, err.Error())
		return export, err
	}
	m.emitLog(id, fmt.Sprintf("已接收 %s → %s", remotePath, export))
	return export, nil
}

func (m *Manager) ListTransferFiles(ctx context.Context, id, remoteDir string) ([]TransferFile, error) {
	m.maintenance.RLock()
	defer m.maintenance.RUnlock()
	dir, err := adb.SharedStoragePath(remoteDir)
	if err != nil {
		return nil, err
	}
	ports, err := m.resolveRunningPorts([]string{id})
	if err != nil {
		return nil, err
	}
	backend, ok := m.adb.(fileADB)
	if !ok {
		return nil, errors.New("当前 ADB 后端不支持文件传输")
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	entries, err := backend.TransferList(ctx, ports[id], dir)
	if err != nil {
		m.emitLog(id, fmt.Sprintf("读取目录 %s 失败: %v", dir, err))
		return nil, err
	}
	files := make([]TransferFile, 0, len(entries))
	for _, e := range entries {
		files = append(files, TransferFile{Name: e.Name, IsDir: e.IsDir})
	}
	return files, nil
}
