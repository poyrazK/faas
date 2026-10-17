package objectqualification

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/objectnode"
)

type transferFile struct {
	Path      string
	Size      int64
	Directory bool
}

func backupFiles(root string) ([]transferFile, error) {
	var files []transferFile
	var total int64
	err := filepath.WalkDir(root, func(path string, e fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path == root {
			return nil
		}
		if e.Type().IsRegular() {
			info, err := e.Info()
			if err != nil {
				return err
			}
			name, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			files = append(files, transferFile{Path: name, Size: info.Size()})
			total += info.Size()
			if len(files) > api.ObjectNodeBackupMaxEntries || total > api.ObjectQualificationDiskBudget {
				return errors.New("qualification backup transfer exceeds budget")
			}
		} else if e.IsDir() {
			name, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			files = append(files, transferFile{Path: name, Directory: true})
		} else {
			return errors.New("qualification backup contains an unsafe file")
		}
		if len(files) > api.ObjectNodeBackupMaxEntries {
			return errors.New("qualification backup transfer exceeds entry budget")
		}
		return nil
	})
	return files, err
}

func writeTransferDirectory(root, name string) error {
	if !filepath.IsLocal(name) {
		return errors.New("invalid backup directory")
	}
	r, err := os.OpenRoot(root)
	if err != nil {
		return err
	}
	defer func() { _ = r.Close() }()
	return r.MkdirAll(name, 0o700)
}

// A Root prevents escapes through path components. Imports are private fresh
// files; aliases, replacement, out-of-order writes and oversized chunks fail.
func writeTransfer(root, name string, offset int64, data []byte) error {
	if !filepath.IsLocal(name) || offset < 0 || offset > api.ObjectQualificationDiskBudget || int64(len(data)) > api.ObjectQualificationDiskBudget-offset || len(data) > api.ObjectQualificationAgentChunkBytes {
		return errors.New("invalid backup chunk")
	}
	r, err := os.OpenRoot(root)
	if err != nil {
		return err
	}
	defer func() { _ = r.Close() }()
	if err = r.MkdirAll(filepath.Dir(name), 0o700); err != nil {
		return err
	}
	flags := os.O_WRONLY | syscall.O_NOFOLLOW
	if offset == 0 {
		flags |= os.O_CREATE | os.O_EXCL
	}
	f, err := r.OpenFile(name, flags, 0o600)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		return err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || !info.Mode().IsRegular() || stat.Nlink != 1 || info.Size() != offset {
		return errors.New("invalid backup chunk identity or offset")
	}
	_, err = f.WriteAt(data, offset)
	return err
}

func readTransfer(root, name string, offset int64) ([]byte, error) {
	if !filepath.IsLocal(name) || offset < 0 {
		return nil, errors.New("invalid backup chunk selector")
	}
	r, err := os.OpenRoot(root)
	if err != nil {
		return nil, err
	}
	defer func() { _ = r.Close() }()
	f, err := r.OpenFile(name, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || !info.Mode().IsRegular() || stat.Nlink != 1 || offset > info.Size() {
		return nil, errors.New("unsafe backup source")
	}
	data := make([]byte, min(int64(api.ObjectQualificationAgentChunkBytes), info.Size()-offset))
	n, err := f.ReadAt(data, offset)
	if errors.Is(err, io.EOF) {
		err = nil
	}
	return data[:n], err
}

func (s *agentState) transfer(ctx context.Context, r agentRequest) (agentResponse, error) {
	importDir, exportDir := filepath.Join(s.lab.root, "import"), filepath.Join(s.lab.root, "export")
	switch r.Operation {
	case "put", "mkdir":
		if int64(len(r.Data)) > api.ObjectQualificationDiskBudget-s.importBytes || r.Offset == 0 && s.importFiles >= api.ObjectNodeBackupMaxEntries {
			return agentResponse{}, errors.New("import exceeds qualification transfer budget")
		}
		if err := os.Mkdir(importDir, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
			return agentResponse{}, err
		}
		var err error
		if r.Operation == "mkdir" {
			err = writeTransferDirectory(importDir, r.Path)
		} else {
			err = writeTransfer(importDir, r.Path, r.Offset, r.Data)
		}
		if err == nil {
			s.importBytes += int64(len(r.Data))
			if r.Offset == 0 {
				s.importFiles++
			}
		}
		return agentResponse{}, err
	case "export":
		if _, err := objectnode.Backup(ctx, s.lab.primaryDir, exportDir); err != nil {
			return agentResponse{}, err
		}
		files, err := backupFiles(exportDir)
		s.files = files
		return agentResponse{}, err
	case "next":
		if len(s.files) == 0 {
			return agentResponse{Done: true}, nil
		}
		s.current, s.files = s.files[0], s.files[1:]
		return agentResponse{Path: s.current.Path, Size: s.current.Size, Directory: s.current.Directory}, nil
	case "read":
		if r.Path == "" || r.Path != s.current.Path {
			return agentResponse{}, errors.New("backup source is not selected")
		}
		data, err := readTransfer(exportDir, r.Path, r.Offset)
		return agentResponse{Data: data}, err
	}
	return agentResponse{}, errors.New("invalid backup transfer operation")
}

func (r *remoteAgent) uploadBackup(ctx context.Context, root string) error {
	files, err := backupFiles(root)
	if err != nil {
		return err
	}
	for _, file := range files {
		if file.Directory {
			if _, err := r.call(ctx, agentRequest{Operation: "mkdir", Path: file.Path}); err != nil {
				return err
			}
			continue
		}
		for offset := int64(0); ; {
			data, err := readTransfer(root, file.Path, offset)
			if err != nil {
				return err
			}
			if _, err = r.call(ctx, agentRequest{Operation: "put", Path: file.Path, Offset: offset, Data: data}); err != nil {
				return err
			}
			offset += int64(len(data))
			if offset == file.Size {
				break
			}
			if len(data) == 0 {
				return io.ErrUnexpectedEOF
			}
		}
	}
	return nil
}

func (r *remoteAgent) downloadBackup(ctx context.Context, root string) error {
	if err := os.Mkdir(root, 0o700); err != nil {
		return err
	}
	if _, err := r.call(ctx, agentRequest{Operation: "export"}); err != nil {
		return err
	}
	var total int64
	for count := 0; count <= api.ObjectNodeBackupMaxEntries; count++ {
		file, err := r.call(ctx, agentRequest{Operation: "next"})
		if err != nil {
			return err
		}
		if file.Done {
			_, err = objectnode.VerifyBackup(ctx, root)
			return err
		}
		if file.Directory {
			if err = writeTransferDirectory(root, file.Path); err != nil {
				return err
			}
			continue
		}
		if file.Size < 0 || file.Size > api.ObjectQualificationDiskBudget-total {
			return errors.New("remote backup exceeds qualification budget")
		}
		total += file.Size
		for offset := int64(0); ; {
			chunk, err := r.call(ctx, agentRequest{Operation: "read", Path: file.Path, Offset: offset})
			if err != nil {
				return err
			}
			if int64(len(chunk.Data)) > file.Size-offset {
				return errors.New("remote backup chunk exceeds declared file size")
			}
			if err = writeTransfer(root, file.Path, offset, chunk.Data); err != nil {
				return err
			}
			offset += int64(len(chunk.Data))
			if offset == file.Size {
				break
			}
			if len(chunk.Data) == 0 {
				return io.ErrUnexpectedEOF
			}
		}
	}
	return errors.New("remote backup exceeds entry budget")
}
