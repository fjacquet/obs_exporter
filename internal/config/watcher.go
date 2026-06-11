package config

import (
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/fsnotify/fsnotify"
	log "github.com/sirupsen/logrus"
)

// Watcher reloads and revalidates the config on SIGHUP or file change, emitting the
// new *Config on Updates(). A bad reload is logged and dropped (the running config stays).
type Watcher struct {
	path    string
	base    string
	fsw     *fsnotify.Watcher
	sigs    chan os.Signal
	updates chan *Config
	done    chan struct{}
}

// NewWatcher starts watching path. Call Close to stop.
func NewWatcher(path string) (*Watcher, error) {
	path = filepath.Clean(path)
	fsw, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}
	// Watch the parent directory rather than the config file's inode: editors and
	// config managers commonly update files by writing a temp file and renaming it
	// over the original, which replaces the inode. A direct file watch would stay
	// bound to the dead inode and silently stop reloading; a directory watch keeps
	// firing, and we filter its events down to our basename.
	if err := fsw.Add(filepath.Dir(path)); err != nil {
		_ = fsw.Close()
		return nil, err
	}
	w := &Watcher{
		path: path, base: filepath.Base(path), fsw: fsw,
		sigs:    make(chan os.Signal, 1),
		updates: make(chan *Config, 1),
		done:    make(chan struct{}),
	}
	signal.Notify(w.sigs, syscall.SIGHUP)
	go w.loop()
	return w, nil
}

// Updates is the channel of successfully reloaded configs.
func (w *Watcher) Updates() <-chan *Config { return w.updates }

// Trigger forces a reload (used by tests and callers that want a manual refresh).
func (w *Watcher) Trigger() { w.reload() }

func (w *Watcher) loop() {
	for {
		select {
		case <-w.done:
			return
		case <-w.sigs:
			w.reload()
		case ev := <-w.fsw.Events:
			if filepath.Base(ev.Name) != w.base {
				continue // an event for some other file in the directory
			}
			// Create/Write cover in-place edits and the temp-file rename landing as
			// our basename; Rename covers the original being renamed away.
			if ev.Op&(fsnotify.Write|fsnotify.Create|fsnotify.Rename) != 0 {
				w.reload()
			}
		case err := <-w.fsw.Errors:
			log.WithError(err).Warn("config watch error")
		}
	}
}

func (w *Watcher) reload() {
	cfg, err := Load(w.path)
	if err != nil {
		log.WithError(err).Warn("config reload failed; keeping current config")
		return
	}
	select {
	case w.updates <- cfg:
	default: // drop if a pending update is unread
	}
}

// Close stops the watcher.
func (w *Watcher) Close() error {
	close(w.done)
	signal.Stop(w.sigs)
	return w.fsw.Close()
}
