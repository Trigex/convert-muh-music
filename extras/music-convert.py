#!/usr/bin/env python3
# This script mass converts an audio library to the given format, preserving already lossily compressed files.
# It depends upon ffmpeg (compiled with --with-fdk-aac for AAC)
# Used as my temporary solution before I complete my Golang version
import os
import os.path
import shutil
import sys
import pathlib
import subprocess
import concurrent.futures
import argparse
import time
import datetime
import signal, psutil

# cli arg parser
parser = argparse.ArgumentParser(description='Music library mass conversion script')

parser.add_argument('output_format', metavar='<output_format>', type=str, help="The format to convert lossless audio files to")
parser.add_argument('input_directory', metavar="<input_directory>", type=str, help="The path to the source music library")
parser.add_argument('output_directory', metavar="<output_directory>", type=str, help="The path to output the converted music library to")
parser.add_argument('-i', '--ignore-lossy', action='store_true', required=False, help="Ignore lossy audio files completely instead of copying them to the output library")
parser.add_argument('-r', '--reencode-lossy', action='store_true', required=False, help="Transcode lossy audio files to the output format instead of copying them to the output library (not recommended)")
parser.add_argument('-f', '--print-formats', action='store_true', required=False, help="Print a list of available output formats and exit")
parser.add_argument('-b', '--bitrate', metavar="bitrate", required=False, type=int, help="The bitrate in kilobits to use for the transcoded audio files")
parser.add_argument('-w', '--workers', metavar="workers", required=False, type=int, help="The number of workers used to encode (roughly CPUs * 5 by default)")

cli_args = parser.parse_args()

# evil global for the worker pool so it can be stopped on the sigint handler
pool = None

# original sigint handler global so terminal doesn't shit itself
original_sigint = None
original_sigterm = None

# container representing a given song to process
class Song:
    def __init__(self, source_path, output_path, type):
        self.source_path = source_path
        self.output_path = output_path
        self.type = type

# container representing an audio format output
class Format:
    def __init__(self, name, is_lossy, codec, bitrate, extension):
        # The name of the format
        self.name = name
        # Is the format lossy?
        self.is_lossy = is_lossy
        # the ffmpeg codec to use for the format
        self.codec = codec
        # the prefered bitrate in kilobits (based off of rough equivilancy to 320k MP3 quality. 0 for no preference)
        self.bitrate = bitrate
        # the format's file extension (including the .)
        self.extension = extension

# Supported audio formats
audio_formats = [
        Format("mp3", True, "libmp3lame", 320,  ".mp3"),
        Format("aac", True, "libfdk_aac", 256, ".m4a"),
        Format("vorbis", True, "libvorbis", 192, ".ogg"),
        Format("opus", True, "libopus", 128, ".opus"),
        Format("flac", False, "", 0, ".flac"),
        Format("wav", False, "", 0, ".wav"),
        Format("alac", False, "alac", 0, ".m4a"),
        Format("aiff", False, "aiff", 0, ".aiff"),
]

# Blacklisted directorties in the input directory (move this to cli or conf file at some point)
directory_blacklist = [
    "PioneerDJ", 
    "Ableton"
]

def is_directory_blacklisted(path):
    is_blacklisted = False
    for directory in directory_blacklist:
        if directory in path:
            is_blacklisted = True
    return is_blacklisted

def is_format_supported(name):
    # get Format object matching name
    format = next((format for format in audio_formats if format.name == name), None)
    if format == None:
        return False
    else:
        return True

def get_format_from_name(name):
    # get Format object matching name
    return next((format for format in audio_formats if format.name == name), None)
    
def is_extension_lossy(extension):
    # get Format object matching extension
    format = next((format for format in audio_formats if format.extension == extension), None)
    if format == None:
        return False
    else:
        return format.is_lossy

def is_file_audio(suffix):
    if suffix == ".mp3" or suffix == ".m4a" or suffix == ".ogg" or suffix == ".opus" or suffix == ".mp2" or suffix == ".aac" or suffix == ".flac" or suffix == ".wav" or suffix == ".alac" or suffix == ".ape" or suffix == ".webm" or suffix == ".aiff":
        return True
    return False

def print_formats():
    return

def kill_child_processes(parent_pid, sig=signal.SIGTERM):
    try:
        parent = psutil.Process(parent_pid)
    except psutil.NoSuchProcess:
        return
    children = parent.children(recursive=True)
    for process in children:
        process.send_signal(sig)

def exit_gracefully(signum, frame):
    print("Exiting script before completion...")
    if signum == 2: # sigint
        global original_sigint
        # restore original sigint
        signal.signal(signal.SIGINT, original_sigint)
    elif signum == 15: # sigterm
        global original_sigterm
        # restore original sigint
        signal.signal(signal.SIGINT, original_sigterm)
    # shutdown the job pool if it actually exists yet
    if(type(pool) == concurrent.futures.ProcessPoolExecutor):
        pool.shutdown(wait=False)
    # kill remaining child processes
    kill_child_processes(os.getpid())
    # abnormal exit`    
    sys.exit(1)

# A task to process a given song
def process_song(song, output_format, bitrate, task_num, total_tasks):
    print("[{} / {}] Processing {}".format(task_num, total_tasks, song.source_path))
    # record task starting time
    start_time = time.time()

    # create output path
    pathlib.Path(os.path.dirname(song.output_path)).mkdir(parents=True, exist_ok=True) 
    # just copy already lossy file
    if is_extension_lossy(song.type) and not cli_args.reencode_lossy:
        shutil.copyfile(song.source_path, song.output_path)
        print("Copied already lossy file " + song.source_path + " to " + song.output_path)
        return True
    else:
        # form ffmpeg command that subprocesses will run
        args = [
            "ffmpeg",
            "-y", ""
            "-i", str(song.source_path),
        ]
        
        # set codec (if no prefered codec, don't set -c:a param)
        if not output_format.codec == "":
            args.extend(["-c:a", output_format.codec])

        # the aac codec is weird and requires "c:v copy" I guess because m4a container shit
        if output_format.name == "aac":
            args.extend(["-c:v", "copy"])

        # set constant bitrate to encode at (if no prefered bitrate, don't set -b:a param)
        if not bitrate == 0:
            args.extend(["-b:a", str(bitrate) + "k"])

        # rest of the parameters
        args.extend([
            "-map_metadata", "0", # map metadata I guess lol it's required for id3
            "-id3v2_version", str(3), # latest n greatest
            str(song.output_path) # output
        ])

        # spawn ffmpeg process
        proc = subprocess.Popen(args, stdout=subprocess.DEVNULL, stderr=subprocess.STDOUT)
        proc.wait()
        
        if proc.returncode != 0:
            print("While encoding {}, an error occured!".format(song.source_path))
            return False
        else:
            total_time = time.time() - start_time
            print("Encoded {} to {} in {}".format(song.source_path, song.output_path, str(datetime.timedelta(seconds=int(total_time)))))
            return True

# DRIVER CODE
def main():
    if cli_args.print_formats:
        print_formats()
        sys.exit(0)

    #inputs
    input_directory = cli_args.input_directory
    output_directory = cli_args.output_directory

    output_format = cli_args.output_format
    # convert output format name if it's a common alias
    if output_format == "ogg":
        output_format = "vorbis"
    elif output_format == "m4a": # just assume aac here
        output_format = "aac"
    # if output format isn't supported
    if not is_format_supported(output_format):
        print("Unsupported or invalid output format! (View list of supported formats with -f)")
        os.exit(1)

    # set output format to it's Format object representation
    output_format = get_format_from_name(output_format)

    if cli_args.bitrate:
        bitrate = cli_args.bitrate
    else:
        bitrate = output_format.bitrate

    if cli_args.workers:
        workers = cli_args.workers
    else:
        workers = None # (Defaults to CPUs * 5)

    # validate inputs
    # if input directory doesn't exist
    if not os.path.exists(os.path.dirname(input_directory)):
        print("The input directory doesn't exist!")
        os.exit(1)

    # create output directory if it doesn't exist
    if not os.path.exists(os.path.dirname(output_directory)):
        os.mkdir(output_directory)

    # List of all songs to process (populated dynamically)
    songs = []

    # walk input directory to find files to process
    for root, directories, files in os.walk(input_directory):
        # files in the directory
        for file in files:
            # split filename into prefix & suffix
            prefix, suffix = os.path.splitext(file)
            # ensure the file is indeed audio (or at least a supported input format)
            if is_file_audio(suffix):
                file_path = root + '/' + file
                output_root = root.replace(input_directory, output_directory)
                # ensure directory isn't blacklisted
                if not is_directory_blacklisted(output_root):
                    # if the file is already lossy retain the suffix
                    if is_extension_lossy(suffix) and not cli_args.reencode_lossy:
                        # break if ignoring lossy files
                        if cli_args.ignore_lossy:
                            break
                        output_path = output_root + '/' + prefix + suffix
                    # if file isn't lossy use output format extension
                    else:
                        output_path = output_root + '/' + prefix + output_format.extension
                    # if the file already exists, no need to reprocess it
                    if not os.path.isfile(output_path):
                        print("Input: " + file_path)
                        songs.append(Song(file_path, output_path, suffix))

    if not songs:
        print("No songs were found, or all the input files have already been proccessed to the output directory")
        sys.exit(1)
    else:
        # record starting time
        start_time = time.time()
        # create process pool
        global pool
        pool = concurrent.futures.ProcessPoolExecutor(max_workers=workers)
        
        tasks = []
        for index, song in enumerate(songs, start=1):
            tasks.append(pool.submit(process_song, song, output_format, bitrate, index, len(songs)))

        for task in concurrent.futures.as_completed(tasks):
            task.result()

        # free pool
        pool.shutdown()

        total_time = time.time()-start_time
        
        print('Processed {} files in {}'.format(len(songs)+1, str(datetime.timedelta(seconds=int(total_time)))))
        sys.exit(0)

if __name__ == '__main__':
    # handle ctrl+c
    original_sigint = signal.getsignal(signal.SIGINT)
    original_sigterm = signal.getsignal(signal.SIGTERM)
    signal.signal(signal.SIGINT, exit_gracefully)
    signal.signal(signal.SIGTERM, exit_gracefully)
    
    main()
