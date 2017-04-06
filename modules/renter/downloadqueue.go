package renter

import (
	"errors"
	"sync/atomic"

	"github.com/NebulousLabs/Sia/modules"
	"github.com/NebulousLabs/Sia/types"
)

const (
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
	var d *download
	if cindex == MAX_UINT64 {
		d = r.newDownload(file, destination, currentContracts)
	} else {
		// Check whether the chunk index is valid.
		numChunks := file.numChunks()
		if cindex < 0 && cindex >= numChunks {
			emsg := "chunk index not in range of stored chunks. Max chunk index = " + string(numChunks-1)
			return errors.New(emsg)
		}
		d = r.newChunkDownload(file, destination, currentContracts, cindex)
	}

	lockID = r.mu.Lock()
	r.log.Printf("Appending to download queue: %+v\n", d)
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
		d := r.downloadQueue[len(r.downloadQueue)-i-1]
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
