package web

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/z354prance/undermynd/internal/config"
	"github.com/z354prance/undermynd/internal/secret"
)

// Settings backup and restore (Settings › Server): every setting, API keys and
// passwords included, exported as one file locked with a passphrase
// (AES-256-GCM, the key from the passphrase by PBKDF2-SHA256), so it can be
// kept anywhere and brought back on this or another install. The sign-in
// password, sessions and the database are not in it.

const (
	backupFormat = "hyvemynd-settings"
	backupIter   = 600000
)

type settingsBackup struct {
	Format  string    `json:"format"`
	Version int       `json:"version"`
	Created time.Time `json:"created"`
	Iter    int       `json:"iterations"`
	Salt    string    `json:"salt"`
	Data    string    `json:"data"` // nonce + sealed settings
}

func backupKey(pass string, salt []byte, iter int) ([]byte, error) {
	return pbkdf2.Key(sha256.New, pass, salt, iter, 32)
}

// plainSettings is the settings as JSON with every secret readable, so the
// file does not depend on this install's secret.key.
func plainSettings(s config.Settings) ([]byte, error) {
	b, err := json.Marshal(s)
	if err != nil {
		return nil, err
	}
	var v any
	if err := json.Unmarshal(b, &v); err != nil {
		return nil, err
	}
	var walk func(any) (any, error)
	walk = func(x any) (any, error) {
		switch t := x.(type) {
		case map[string]any:
			for k, e := range t {
				n, err := walk(e)
				if err != nil {
					return nil, err
				}
				t[k] = n
			}
		case []any:
			for i, e := range t {
				n, err := walk(e)
				if err != nil {
					return nil, err
				}
				t[i] = n
			}
		case string:
			if strings.HasPrefix(t, "enc:") {
				return secret.Decrypt(t)
			}
		}
		return x, nil
	}
	v, err = walk(v)
	if err != nil {
		return nil, err
	}
	return json.Marshal(v)
}

func sealBackup(plain []byte, pass string) (settingsBackup, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return settingsBackup{}, err
	}
	key, err := backupKey(pass, salt, backupIter)
	if err != nil {
		return settingsBackup{}, err
	}
	blk, _ := aes.NewCipher(key)
	g, _ := cipher.NewGCM(blk)
	nonce := make([]byte, g.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return settingsBackup{}, err
	}
	return settingsBackup{Format: backupFormat, Version: 1, Created: time.Now().UTC(), Iter: backupIter,
		Salt: base64.StdEncoding.EncodeToString(salt), Data: base64.StdEncoding.EncodeToString(g.Seal(nonce, nonce, plain, []byte(backupFormat)))}, nil
}

var errBadPass = errors.New("that passphrase does not open this file")

func openBackup(f settingsBackup, pass string) ([]byte, error) {
	if f.Format != backupFormat {
		return nil, errors.New("this is not a HyveMynd settings file")
	}
	if f.Version != 1 {
		return nil, fmt.Errorf("this settings file is version %d; this HyveMynd reads version 1", f.Version)
	}
	salt, err1 := base64.StdEncoding.DecodeString(f.Salt)
	data, err2 := base64.StdEncoding.DecodeString(f.Data)
	if err1 != nil || err2 != nil || f.Iter < 10000 || f.Iter > 10000000 {
		return nil, errors.New("the settings file is damaged")
	}
	key, err := backupKey(pass, salt, f.Iter)
	if err != nil {
		return nil, err
	}
	blk, _ := aes.NewCipher(key)
	g, _ := cipher.NewGCM(blk)
	if len(data) < g.NonceSize() {
		return nil, errors.New("the settings file is damaged")
	}
	plain, err := g.Open(nil, data[:g.NonceSize()], data[g.NonceSize():], []byte(backupFormat))
	if err != nil {
		return nil, errBadPass
	}
	return plain, nil
}

func (s *Server) adminSettingsExport(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Passphrase string `json:"passphrase"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	if len([]rune(in.Passphrase)) < 8 {
		jsonErr(w, http.StatusBadRequest, "choose a passphrase of at least 8 characters")
		return
	}
	plain, err := plainSettings(s.Cfg.Get())
	if err != nil {
		jsonErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	f, err := sealBackup(plain, in.Passphrase)
	if err != nil {
		jsonErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	name := "hyvemynd-settings-" + time.Now().Format("2006-01-02") + ".json"
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
	s.Log.Printf("settings: exported a backup")
	writeJSON(w, f)
}

// adminSettingsImport opens a backup and says what it would change; with
// apply it replaces the settings, keeping a copy of the old ones.
func (s *Server) adminSettingsImport(w http.ResponseWriter, r *http.Request) {
	var in struct {
		File       settingsBackup `json:"file"`
		Passphrase string         `json:"passphrase"`
		Apply      bool           `json:"apply"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4<<20)
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		jsonErr(w, http.StatusBadRequest, "that file could not be read")
		return
	}
	plain, err := openBackup(in.File, in.Passphrase)
	if err != nil {
		jsonErr(w, http.StatusBadRequest, err.Error())
		return
	}
	var next config.Settings
	if err := json.Unmarshal(plain, &next); err != nil {
		jsonErr(w, http.StatusBadRequest, "the settings in the file could not be read: "+err.Error())
		return
	}
	cur := s.Cfg.Get()
	changes := settingsChanges(cur, next)
	out := map[string]any{"created": in.File.Created, "changes": changes}
	if !in.Apply {
		writeJSON(w, out)
		return
	}
	// Keep the settings being replaced next to the settings file.
	if old, err := json.MarshalIndent(cur, "", "  "); err == nil {
		bak := s.Cfg.Env.ConfigDir + "/settings.before-restore-" + time.Now().Format("20060102-150405") + ".json"
		if err := os.WriteFile(bak, old, 0o600); err == nil {
			out["savedOld"] = bak
		}
	}
	if err := s.Cfg.Set(next); err != nil {
		jsonErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if s.VPN != nil && s.VPN.Tunnel != nil && !reflect.DeepEqual(cur.VPN, next.VPN) {
		s.VPN.Tunnel.Restart()
	}
	s.Log.Printf("settings: restored from a backup made %s (%d sections changed)", in.File.Created.Format(time.RFC3339), len(changes))
	out["applied"] = true
	writeJSON(w, out)
}

// settingsChanges names the sections of the settings that differ, in words.
func settingsChanges(a, b config.Settings) []string {
	ja, _ := plainSettings(a)
	jb, _ := plainSettings(b)
	var ma, mb map[string]json.RawMessage
	_ = json.Unmarshal(ja, &ma)
	_ = json.Unmarshal(jb, &mb)
	names := map[string]string{
		"categories": "Categories (Sonarr, Radarr, Lidarr and the rest)", "torbox": "TorBox", "usenet": "Usenet providers", "vpn": "VPN and torrents",
		"tdarr": "Tdarr", "playback": "Media server and Tracearr", "requests": "Public requests and Seerr", "bookorbit": "BookOrbit",
		"metadata": "Metadata (TMDB)", "feeds": "Feeds", "rules": "Feed rules", "watches": "Watch folders", "donations": "Donate", "access": "Access requests",
		"speed": "Speed limits", "qbit": "Client folder", "attention": "Upgrades", "intakePaused": "Intake paused",
		"clientUser": "Client login for the arrs", "clientPassword": "Client login for the arrs", "stallMinutes": "Timing",
		"reconcileSeconds": "Timing", "flagImportHours": "Timing", "skipQualityCheck": "Quality check",
	}
	var out []string
	named := map[string]bool{}
	seen := map[string]bool{}
	for k := range ma {
		seen[k] = true
	}
	for k := range mb {
		seen[k] = true
	}
	for k := range seen {
		if string(ma[k]) == string(mb[k]) || (isEmptyJSON(ma[k]) && isEmptyJSON(mb[k])) {
			continue
		}
		n := names[k]
		if n == "" {
			n = k
		}
		if !named[n] {
			named[n] = true
			out = append(out, n)
		}
	}
	sort.Strings(out)
	return out
}

func isEmptyJSON(m json.RawMessage) bool {
	switch strings.TrimSpace(string(m)) {
	case "", "null", "{}", "[]", `""`, "false", "0":
		return true
	}
	return false
}
