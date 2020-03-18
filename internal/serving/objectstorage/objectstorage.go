package objectstorage

import (
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"time"

	log "github.com/sirupsen/logrus"

	"gitlab.com/gitlab-org/gitlab-pages/internal/httperrors"
	"gitlab.com/gitlab-org/gitlab-pages/internal/serving"
	"gitlab.com/gitlab-org/gitlab-pages/internal/zip"
)

// ErrKeyNotFound TODO update doc
var ErrKeyNotFound = errors.New("key not found")

type Provider string

const (
	ProviderS3  Provider = "s3"
	ProviderGCS Provider = "gcs"
)

type ObjectStorage interface {
	GetObject(path string) (Object, error)
}

type Object interface {
	ReaderAt() (io.ReaderAt, error)
	Reader() io.Reader
	Name() string
	Size() int64
	ModTime() time.Time
	ContentType() string
	Close() error
}

type Client struct {
	bucket   string
	provider ObjectStorage
	// TODO: cache zip files by projectID for now, will need to expire/cleanup
	cacheMux      sync.Mutex
	cachedReaders map[uint64]*zip.Reader
}

func New(provider ObjectStorage) *Client {
	return &Client{
		provider:      provider,
		cacheMux:      sync.Mutex{},
		cachedReaders: map[uint64]*zip.Reader{},
	}
}

func (c *Client) ServeFileHTTP(handler serving.Handler) bool {
	served, err := c.tryZipFile(handler)
	if err != nil {
		log.WithError(err).Error("file not found in archive")
		return false
	}
	if !served {
		if err := c.serveFile(handler); err != nil {
			return false
		}
	}

	return true
}

func (c *Client) ServeNotFoundHTTP(handler serving.Handler) {
	httperrors.Serve404(handler.Writer)
}

func endsWithSlash(path string) bool {
	return strings.HasSuffix(path, "/")
}

func (c *Client) serveFile(handler serving.Handler) error {
	// TODO validate different paths like disk/Reader.tryFile
	fileName := strings.TrimSuffix(handler.LookupPath.Path, "/") + "/"
	if handler.SubPath != "" {
		fileName += handler.SubPath
	}

	if endsWithSlash(fileName) {
		fileName += "index.html"
	}
	object, err := c.provider.GetObject(fileName)
	if err != nil {
		if err == ErrKeyNotFound {
			return nil
		}
		return err
	}
	defer object.Close()
	err = writeContent(handler, object.Reader(), object.Name(), object.ModTime(), object.ContentType())
	if err != nil {
		return err
	}
	return nil
}

func (c *Client) tryZipFile(handler serving.Handler) (bool, error) {
	projectID := handler.LookupPath.ProjectID
	c.cacheMux.Lock()
	reader, ok := c.cachedReaders[projectID]
	c.cacheMux.Unlock()
	if ok && reader == nil {
		// cached zip not found
		// TODO need to expire the cache
		return false, nil
	} else if !ok {
		// TODO assume the API returns the base path of the project and we'll look for artifact.zip
		obj, err := c.provider.GetObject(handler.LookupPath.Path + "artifactssssss.zip")
		if err != nil {
			if err == ErrKeyNotFound {
				c.cachedReaders[projectID] = nil
				// could not find zip file
				return false, nil
			}
			return false, fmt.Errorf("failed to get object: %w", err)
		}

		r, err := obj.ReaderAt()
		if err != nil {
			return false, err
		}
		reader, err = zip.New(r, obj.Size())
		if err != nil {
			return false, fmt.Errorf("failed create zip.Reader: %w", err)
		}

		c.cacheMux.Lock()
		c.cachedReaders[projectID] = reader
		c.cacheMux.Unlock()
	}

	filename := handler.SubPath
	if filename == "" {
		filename = "index.html"
	}

	file, stat, err := reader.Open(filename)
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			return false, nil
		}
		return false, fmt.Errorf("failed to open file: %w", err)
	}
	defer file.Close()
	contentType := mime.TypeByExtension(filepath.Ext(stat.Name()))
	err = writeContent(handler, file, stat.Name(), stat.ModTime(), contentType)
	return err == nil, err
}

func writeContent(handler serving.Handler, content io.Reader, fileName string, modTime time.Time, contentType string) error {
	if content == nil {
		return nil
	}

	w := handler.Writer
	if !handler.LookupPath.HasAccessControl {
		// Set caching headers
		w.Header().Set("Cache-Control", "max-age=600")
		w.Header().Set("Expires", time.Now().Add(10*time.Minute).Format(time.RFC1123))
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Last-Modified", modTime.UTC().Format(http.TimeFormat))
	// TODO implement Seek(offset int64, whence int) (int64, error) so that we can use http.ServeContent?
	var err error
	_, err = io.Copy(w, content.(io.Reader))
	if err != nil {
		return fmt.Errorf("failed to write response: %w", err)
	}

	return nil
}
