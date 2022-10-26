# convert-muh-music

convert-muh-music is a bulk music library transcoder, catered to people wanting to make a clone of their master music library with a different target audio codec.

It has sane defaults other bulk audio transcoders lack, for example, not transcoding already lossily compressed files if the desired output library format is lossy (it instead opts by default to copy the file as is to the new library, as to not lose quality through multigenerational lossy compressions of a file), and on subsequent runs to update the output library with new files from the master library, does not process files that have already been processed before.

This makes convert-muh-music an ideal tool for those who maintain large lossless music libraries, and would like lossy clones of the library for other purposes (In fact, I originally developed it because my music library is mostly flac, but the CDJs I DJ on at parties don't support flac)

While the Golang version in this repo is not fully functional, a prior Python implementation I wrote is included in the /extras directory, and has all the core features implemented.
