package scheduler

import (
	"io/ioutil"
	"math/rand"
	"os"
	"time"

	"github.com/stretchr/testify/require"

	"code.uber.internal/infra/kraken/client/torrent/bencode"
	"code.uber.internal/infra/kraken/client/torrent/meta"
	"code.uber.internal/infra/kraken/client/torrent/storage"
)

const trackerAddr = "localhost:4001"

const testTempDir = "/tmp/kraken_scheduler_test"

func init() {
	rand.Seed(time.Now().UnixNano())
	os.Mkdir(testTempDir, 0755)
}

const chars = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"

func randomText(n int) []byte {
	b := make([]byte, n)
	for i := range b {
		c := chars[rand.Intn(len(chars))]
		b[i] = byte(c)
	}
	return b
}

func genConfig() Config {
	return Config{
		TrackerAddr:                  trackerAddr,
		MaxOpenConnectionsPerTorrent: 20,
		AnnounceInterval:             500 * time.Millisecond,
		DialTimeout:                  5 * time.Second,
		WriteTimeout:                 5 * time.Second,
		// Buffers are just a performance optimization, so a zero-sized
		// buffer will instantly force any deadlock conditions.
		SenderBufferSize:   0,
		ReceiverBufferSize: 0,
	}
}

func genPeerID() PeerID {
	var p PeerID
	if _, err := rand.Read(p[:]); err != nil {
		panic(err)
	}
	return p
}

type tempTorrentManager struct {
	storage.TorrentManager
	tmpdir string
}

func (m *tempTorrentManager) Delete() {
	if err := os.RemoveAll(m.tmpdir); err != nil {
		panic(err)
	}
}

func genTorrentManager() *tempTorrentManager {
	d, err := ioutil.TempDir(testTempDir, "manager_")
	if err != nil {
		panic(err)
	}
	return &tempTorrentManager{storage.NewFileStorage(d), d}
}

// writeTorrent writes the given content into a torrent file into tm's storage.
// Useful for populating a completed torrent before seeding it.
func writeTorrent(tm storage.TorrentManager, mi *meta.TorrentInfo, content []byte) storage.Torrent {
	t, err := tm.CreateTorrent(mi.HashInfoBytes(), mi.InfoBytes)
	if err != nil {
		panic(err)
	}
	if _, err := t.WriteAt(content, 0); err != nil {
		panic(err)
	}
	return t
}

type genTorrentOpts struct {
	Size        int
	PieceLength int
}

func genTorrent(o genTorrentOpts) (mi *meta.TorrentInfo, content []byte) {
	if o.Size == 0 {
		o.Size = 128
	}
	if o.PieceLength == 0 {
		o.PieceLength = 32
	}

	f, err := ioutil.TempFile(testTempDir, "torrent_")
	if err != nil {
		panic(err)
	}
	defer os.Remove(f.Name())

	content = randomText(o.Size)
	if err := ioutil.WriteFile(f.Name(), content, 0755); err != nil {
		panic(err)
	}
	info := meta.Info{
		PieceLength: int64(o.PieceLength),
	}
	if err := info.BuildFromFilePath(f.Name()); err != nil {
		panic(err)
	}
	mi = &meta.TorrentInfo{
		Announce: trackerAddr + "/announce",
	}
	mi.InfoBytes, err = bencode.Marshal(info)
	if err != nil {
		panic(err)
	}
	return mi, content
}

type testTorrent struct {
	Info    *meta.TorrentInfo
	Content []byte
}

func genTestTorrents(n int, o genTorrentOpts) []*testTorrent {
	l := make([]*testTorrent, n)
	for i := range l {
		mi, content := genTorrent(o)
		l[i] = &testTorrent{mi, content}
	}
	return l
}

type testPeer struct {
	Scheduler      *Scheduler
	TorrentManager *tempTorrentManager
}

func genTestPeers(n int, startPort int, config Config) (peers []*testPeer, stopAll func()) {
	peers = make([]*testPeer, n)
	for i := range peers {
		tm := genTorrentManager()
		s, err := New(
			genPeerID(), "localhost", startPort+i, "sjc1", tm, config)
		if err != nil {
			tm.Delete()
			panic(err)
		}
		peers[i] = &testPeer{s, tm}
	}
	return peers, func() {
		for _, p := range peers {
			defer p.Scheduler.Stop()
			defer p.TorrentManager.Delete()
		}
	}
}

func checkContent(r *require.Assertions, t storage.Torrent, expected []byte) {
	result := make([]byte, len(expected))
	_, err := t.ReadAt(result, 0)
	r.NoError(err)
	r.Equal(expected, result)
}