package renter

import (
	"errors"
	"sync/atomic"

	"github.com/NebulousLabs/Sia/modules"
	"github.com/NebulousLabs/Sia/types"
	"fmt"
)

const(
	// Effectively used as flag to indicate that entire file has to be downloaded.
	MAX_UINT64 = ^uint64(0)
)

// Download downloads a file, identified by its path, to the destination
// specified.
func (r *Renter) Download(path, destination string) error {
	return r.DownloadChunk(path, destination, MAX_UINT64)
}

func (r *Renter) DownloadChunk(path, destination string, cindex uint64) error {
	// Lookup the file associated with the nickname.
	lockID := r.mu.RLock()

	fmt.Printf("DownloadChunk: path %s, dst: %s, cindex: %d\n", path, destination, cindex)

	file, exists := r.files[path]
	r.mu.RUnlock(lockID)
	if !exists {
		return errors.New("no file with that path")
	}

	// Build current contracts map.
	currentContracts := make(map[modules.NetAddress]types.FileContractID)
	for _, contract := range r.hostContractor.Contracts() {
		currentContracts[contract.NetAddress] = contract.ID
	}

	// Create the download object and add it to the queue.
	d := r.newDownload(file, destination, currentContracts)

	// Catch error if chunk index is not in file.
	if cindex < 0 && cindex >= d.numChunks && cindex != MAX_UINT64 {
		r.log.Critical("Passed index invalid. Index: %d, numChunks: %d\n", cindex, d.numChunks)

		emsg := fmt.Sprintf("DownloadChunk: Chunk index not in range of stored chunks. Max chunk index = %d\n", d.numChunks - 1)
		return errors.New(emsg)
	} else {
		r.log.Printf("DownloadChunk: Chunk index valid: %d, # chunks: %d", cindex, d.numChunks)
	}

	// Go through the `finishedChunks` list and set all to true except the requested chunk.
	if cindex != MAX_UINT64 {
		for i := range d.finishedChunks {
			d.finishedChunks[i] = uint64(i) != cindex // Set all but bool at `index` to true.
		}
	}

	lockID = r.mu.Lock()
	r.downloadQueue = append(r.downloadQueue, d)
	r.mu.Unlock(lockID)
	r.newDownloads <- d

	// Block until the download has completed.
	//
	// TODO: Eventually just return the channel to the error instead of the
	// error itself.
	select {
	case <-d.downloadFinished:
		return d.Err()
	case <-r.tg.StopChan():
		return errors.New("download interrupted by shutdown")
	}
}

// DownloadQueue returns the list of downloads in the queue.
func (r *Renter) DownloadQueue() []modules.DownloadInfo {
	lockID := r.mu.RLock()
	defer r.mu.RUnlock(lockID)

	// Order from most recent to least recent.
	downloads := make([]modules.DownloadInfo, len(r.downloadQueue))
	for i := range r.downloadQueue {
		d := r.downloadQueue[len(r.downloadQueue) - i - 1]
		downloads[i] = modules.DownloadInfo{
			SiaPath:     d.siapath,
			Destination: d.destination,
			Filesize:    d.fileSize,
			StartTime:   d.startTime,
		}
		downloads[i].Received = atomic.LoadUint64(&d.atomicDataReceived)

		if err := d.Err(); err != nil {
			downloads[i].Error = err.Error()
		}
	}
	return downloads
}
