package main

import (
	"bufio"
	"fmt"
	"io"
	"io/fs"
	"log"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

type job struct {
	// The source audio file to be processed
	sourceFile string
	// The output file to produce
	destinationFile string
	// Should the file be encoded to another format, or just copied to the output path?
	encode bool
	// The format to be used in encodes
	format audioFormat
	//
	options jobOptions
}

type jobReport struct {
	// Exit code of the job's ffmpeg subprocess
	exitCode int
	// Id of the worker who completed the job
	workerId int
	// The job the report is regarding
	job job
	// The amount of time the job took to complete
	elaspedTime time.Duration
	// error
	error error
}

type jobOptions struct {
	bitrate int
	encoder string
}

type audioFormat struct {
	// name of the format
	name string
	// is the codec lossily compressed?
	isLossy bool
	// a list of encoders usable for the codec, with the lowest index being preferred for quality
	encoders []string
	// The preferred bitrate for a quality around equivalent to a 320k MP3
	preferredBitrate int
	// The file extension the format most commonly uses
	fileExtension string
	// any extra ffmpeg arguments the codec might want
	ffmpegArguments []string
}

func audioFormats() []audioFormat {
	return []audioFormat{
		{name: "mp3", isLossy: true, encoders: []string{"libmp3lame", "libshine"}, preferredBitrate: 320, fileExtension: ".mp3"},
		// m4a requires -c:v copy for encodes because reasons I guess detailing with it's container
		{name: "aac", isLossy: true, encoders: []string{"libfdk_aac", "aac"}, preferredBitrate: 256, fileExtension: ".m4a", ffmpegArguments: []string{"-c:v", "copy"}},
		{name: "vorbis", isLossy: true, encoders: []string{"libvorbis", "vorbis"}, preferredBitrate: 192, fileExtension: ".ogg"},
		{name: "opus", isLossy: true, encoders: []string{"libopus"}, preferredBitrate: 128, fileExtension: ".opus"},
		// Lossless formats are in the list in case someone wanted to transcode to different one. No encoder preference or preferred bitrate for them, ffmpeg defaults will be fine
		{name: "flac", isLossy: false, encoders: nil, preferredBitrate: 0, fileExtension: ".flac"},
		{name: "alac", isLossy: false, encoders: nil, preferredBitrate: 0, fileExtension: ".m4a"},
		{name: "aiff", isLossy: false, encoders: nil, preferredBitrate: 0, fileExtension: ".aiff"},
		{name: "wav", isLossy: false, encoders: nil, preferredBitrate: 0, fileExtension: ".wav"},
	}
}

func audioExtensions() []string {
	return []string{
		".mp3",
		".m4a",
		".ogg",
		".opus",
		".mp2",
		".aac",
		".flac",
		".wav",
		".alac",
		".aiff",
		".ape",
		".webm",
		".aiff",
		".mp4",
		".wma",
	}
}

func getAudioFormatFromName(name string) (*audioFormat, error) {
	for _, format := range audioFormats() {
		if format.name == name {
			return &format, nil
		}
	}

	return nil, fmt.Errorf("unknown format %s", name)
}

func isAudioExtension(extension string) bool {
	// list of common audio extensions
	audioExtensions := audioExtensions()

	for _, audioExtension := range audioExtensions {
		if extension == audioExtension {
			return true
		}
	}

	return false
}

func isLossyExtension(extension string) bool {
	for _, format := range audioFormats() {
		if format.fileExtension == extension {
			if format.isLossy {
				return true
			}
		}
	}
	return false
}

func directoryIsBlacklisted(path string, blacklist []string) bool {
	for _, blacklistedDirectory := range blacklist {
		if strings.Contains(path, blacklistedDirectory) {
			return true
		}
	}

	return false
}

func isEncoderAvailable(encoders []string, name string) bool {
	for _, encoder := range encoders {
		if name == encoder {
			return true
		}
	}

	return false
}

func createJobsList(srcDir string, outDir string, format audioFormat, options jobOptions, blacklistedDirectories []string) ([]job, error) {
	var jobs []job

	var err error = filepath.WalkDir(srcDir, func(curPath string, entry fs.DirEntry, err error) error {
		// is file, and it's parent directory isn't blacklisted
		if !entry.IsDir() && !directoryIsBlacklisted(path.Dir(curPath), blacklistedDirectories) {
			extension := filepath.Ext(entry.Name())
			name := strings.TrimSuffix(entry.Name(), extension)

			// is audio file
			if isAudioExtension(extension) {
				outPathBase := strings.ReplaceAll(path.Dir(curPath), srcDir, outDir)
				// Ensure the output file doesn't exist
				_, err := os.Stat(outPathBase + "/" + entry.Name())
				if os.IsNotExist(err) {
					//fmt.Println(outPathBase + "/" + entry.Name() + " doesn't exist!")
					var newJob job
					// don't reencode lossy files
					if isLossyExtension(extension) {
						newJob = job{sourceFile: curPath, destinationFile: outPathBase + "/" + entry.Name(), format: format, options: options, encode: false}
					} else {
						newJob = job{sourceFile: curPath, destinationFile: outPathBase + "/" + name + format.fileExtension, format: format, options: options, encode: true}
					}

					jobs = append(jobs, newJob)
				} else {
					//fmt.Println(outPathBase + "/" + entry.Name() + " already exists!")
				}
			}
		}
		return nil
	})

	return jobs, err
}

func buildFfmpegArgs(format audioFormat, job job, options jobOptions) []string {
	// base arguments
	args := []string{"-loglevel", "error", "-y", "-i", job.sourceFile}

	// if the format specifies a bitrate
	if options.bitrate != 0 {
		args = append(args, "-b:a", fmt.Sprint(options.bitrate)+"k")
	}

	// -c:a
	if options.encoder != "" {
		args = append(args, "-c:a", options.encoder)
	}

	// this is here right now necause the only format which specifies ffmpegArguments in AAC, which needs to be around -b:a
	// positional arguments needs to be figured out
	if format.ffmpegArguments != nil {
		args = append(args, format.ffmpegArguments...)
	}

	// Audio metadata
	args = append(args, "-map_metadata", "0", "-id3v2_version", "3", job.destinationFile)

	return args
}

func getFfmpegEncoders() ([]string, error) {
	out, err := exec.Command("ffmpeg", "-loglevel", "error", "-encoders").Output()
	if err != nil {
		return nil, err
	}

	// Remove first 10 lines of the command output, which only contain legend information for reading the encoder information
	// if I could run tail or something this would be so much nicer but gotta suport le windows hur dur dur
	var lines string
	var scanner *bufio.Scanner
	scanner = bufio.NewScanner(strings.NewReader(string(out)))
	outLength := strings.Count(string(out), "\n")
	for i := 1; i <= outLength; i++ {
		scanner.Scan()
		if i > 10 {
			// make sure the last line has no newline attached
			if i == outLength {
				lines = lines + scanner.Text()
			} else {
				lines = lines + scanner.Text() + "\n"
			}
		}
	}

	var encoders []string
	scanner = bufio.NewScanner(strings.NewReader(lines))

	for scanner.Scan() {
		words := strings.Fields(scanner.Text())
		// just in case of error where data isn't as we expect
		if len(words) > 1 {
			// Append second word of string, the encoder
			encoders = append(encoders, strings.Fields(scanner.Text())[1])
		}
	}
	return encoders, nil
}

// worker goroutine, of which we'll run several
// concurrent instances, these workers will receive
// work on the jobs channel and send the corresponding
// results on results.
func worker(id int, jobs <-chan job, results chan<- jobReport) {
	for j := range jobs {
		var err error
		var cmd *exec.Cmd
		var errLogger io.ReadCloser
		var errMsg string
		var exitCode int
		var ffmpegArgs []string

		startTime := time.Now()

		// Create output directory
		if err = os.MkdirAll(path.Dir(j.destinationFile), os.ModePerm); err != nil {
		}

		// Only a copy job
		if !j.encode {
			// Source file handle
			fileHandleIn, err := os.Open(j.sourceFile)
			if err != nil {
				results <- jobReport{error: err}
			}

			// Output file handle
			fileHandleOut, err := os.Create(j.destinationFile)
			if err != nil {
				results <- jobReport{error: err}
			}

			_, err = io.Copy(fileHandleOut, fileHandleIn)

			if err != nil {
				results <- jobReport{error: err}
			}

			fileHandleOut.Close()
			fileHandleIn.Close()

			elaspedTime := time.Since(startTime)

			results <- jobReport{exitCode: 0, workerId: id, error: err, elaspedTime: elaspedTime, job: j}
		} else { // reencode job
			// build the ffmpeg command to be run
			ffmpegArgs = buildFfmpegArgs(j.format, j, j.options)
			fmt.Println(ffmpegArgs)

			fmt.Println("worker", id, "started job")

			cmd = exec.Command("ffmpeg", ffmpegArgs...)

			// pipe to capture ffmpeg error logging
			errLogger, err = cmd.StderrPipe()

			// Problem establishing stderr pipe
			if err != nil {
				results <- jobReport{error: err}
			}

			// Start ffmpeg process
			if err = cmd.Start(); err != nil {
				results <- jobReport{error: err}
			}

			// Capture from process error logger
			for {
				buf := make([]byte, 1024)
				_, err := errLogger.Read(buf)
				errMsg += string(buf)
				if err != nil {
					break
				}
			}

			cmd.Wait()
			exitCode = cmd.ProcessState.ExitCode()

			elaspedTime := time.Since(startTime)

			if exitCode == 0 {
				err = nil
			} else {
				err = fmt.Errorf("worker %d's execution failed: ffmpeg: %s, exit code: %d", id, strings.Replace(errMsg, "\n", "", -1), exitCode)
			}

			results <- jobReport{exitCode: cmd.ProcessState.ExitCode(), workerId: id, error: err, elaspedTime: elaspedTime, job: j}
		}
	}
}

func main() {
	var err error
	// hardcoded cli args for now
	srcDir := "/Volumes/Futaba/Music"
	destDir := "/Volumes/Futaba/Music Test/"
	formatName := "aac"
	directoryBlacklist := []string{"PioneerDJ", "Various Artists", "Ableton", "Logic"}
	var bitrate int = 32

	// no real speed gains past the number of logical cpus
	workerCount := runtime.NumCPU()

	srcDir, err = filepath.Abs(srcDir)
	if err != nil {
		fmt.Println(err)
	}
	destDir, err = filepath.Abs(destDir)
	if err != nil {
		fmt.Println(err)
	}

	format, err := getAudioFormatFromName(formatName)
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}

	encoders, err := getFfmpegEncoders()
	if err != nil {
		log.Fatal(err)
	}

	// Check if encoders for format are available
	var encoder string
	if format.encoders != nil {
		encoderIsHighestQuality := false
		for i := 0; i < len(format.encoders); i++ {
			if isEncoderAvailable(encoders, format.encoders[i]) {
				encoder = format.encoders[i]

				if i == 0 {
					encoderIsHighestQuality = true
					break
				} else if len(format.encoders)-1 > i { // if there are still more encoders in the list, settle for the highest quality encoder that is available
					break
				}
			}
		}

		if encoder == "" {
			fmt.Printf("An ffmpeg encoder for %s was not found! Please ensure your ffmpeg binary is built with a supported encoder (%v)\n", formatName, format.encoders)
			os.Exit(1)
		}

		if !encoderIsHighestQuality {
			fmt.Printf("The prefered, highest quality %s encoder, %s, wasn't found. Please build ffmpeg with support for %s for the highest quality encoding.\n", format.name, format.encoders[0], format.encoders[0])
		}
	}

	options := new(jobOptions)
	if bitrate != 0 {
		options.bitrate = bitrate
	} else {
		options.bitrate = format.preferredBitrate
	}

	options.encoder = encoder

	jobsList, err := createJobsList(srcDir, destDir, *format, *options, directoryBlacklist)
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}

	//fmt.Println(jobsList)
	//os.Exit(1)

	fmt.Printf("%d jobs added to the job queue\n", len(jobsList))

	jobCount := len(jobsList)
	// buffered channel to send workers jobs
	jobs := make(chan job, jobCount)
	// channel to return results
	results := make(chan jobReport)

	// start up worker goroutines, initially blocked
	for w := 1; w <= workerCount; w++ {
		go worker(w, jobs, results)
	}

	// record starting time
	startTime := time.Now()

	// submit jobs
	for j := 1; j <= jobCount; j++ {
		jobs <- jobsList[j-1]
	}
	close(jobs)

	// collect resulting job reports
	for a := 1; a <= jobCount; a++ {
		jobReport := <-results
		if jobReport.error != nil {
			fmt.Println(jobReport.error)
		} else {
			fmt.Printf("worker %d completed job in %s, outputting %s, exit code: %d\n", jobReport.workerId, jobReport.elaspedTime, jobReport.job.destinationFile, jobReport.exitCode)
		}
	}

	elaspedTime := time.Since(startTime)
	fmt.Printf("All files processed in %s\n", elaspedTime)
}
