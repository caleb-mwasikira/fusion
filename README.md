# Go, and a Simple, RAM-Based File System
**Due: September 12, 2021, 11:59:59 pm, v1.0**

This project is designed to familiarize you with two technologies: the
[Go systems programming language](https://golang.org/ref/spec) from Google, and the Bazil [FUSE](https://en.wikipedia.org/wiki/Filesystem_in_Userspace) interface
for building user-level file systems.
You will use Go and the Bazil/fuse language binding to build a simple
user-space file system (UFS) that supports arbitrary file creations,
reads, writes, directory creates, and deletions.
The UFS will not support persistence, i.e.
the contents of all UFS files will disappear the minute the UFS
process is killed.

![system](system.png)

FUSE is a loadable kernel module; that is, a piece of code that
is compiled and loaded directly into the kernel.
Inside the kernel, potentially many different file systems
co-exist, all living under the *virtual file system* layers, or
VFS for short (see Figure~1).
The VFS layer allows any application to call down into the file system
with the same API, regardless of which, or which type of file system,
is ultimately serving the file requests.
We will use the kernel module, but *will never be hacking inside
the kernel.*

The VFS layer knows which underlying file system to use based on which
part of the file system namespace is being accessed.
For instance, if you have an NFS volume mounted at /mnt/nfs, then the
VFS layer knows to vector any calls to some file
`/mnt/nfs/foo.c` to the NFS client, which will then communicate
with a (possibly remote) NFS server for the actual data.

Likewise, we will mount a FUSE volume somewhere in the file system
namespace (`/tmp/<userid>` is a good default), and any system
calls to files under that directory get vectored to the FUSE module
that lives under the VFS layer.
In turn, the FUSE module will re-direct any calls sent to it back up
to our user-level program.
So all we have to write is the user-level program!


## Setup

This project should be done on linux; Macs are no longer supported by
the fuse library, and windows has all sorts of issues. You can do this
inside a free VM like [Virtualbox](https://www.virtualbox.org/wiki/Downloads)  (though you will need Parallels if on
a new ARM-based Mac). 
crowd. 
Later projects can be done anywhere, including Macs.

Assuming you are using `~/go` as your GOPATH, create directory
`~/go/src/cmsc818ef21`. `cd` into `cmsc818f21` and then clone your
`p1` repository using your
directoryID. For example, this whole example for me would be:
```
cd ~/go/src
mkdir cmsc818f21
cd cmsc818f21
git clone git@gitlab.cs.umd.edu/cmsc818eFall2021/keleher
```
Inside `~/go/src/cmsc818f21/keleher/p1` (w/ your direID) you should now see two files:
`dfs.go`, which you will modify to turn into a full-fledged in-memory
file system, and `hello.go`, which is a simple example file system.

`hellofs.go` is actually runnable. From the
`p1` directory, type:

```
    go mod init
    go mod tidy
    go run hellofs.go 
```
	
and a virtual file system will be created in `/tmp/dss`. `cd` around, `cat` the
file, etc.  Note that this is a
*read-only* file-system.

### Go Resources
We will be using the Go language (referred to as "golang", especially
in web searches), and the Go language bindings for FUSE built by the
`bazil` project.

- [http://golang.org](http://golang.org)
  - [language spec](http://golang.org/ref/spec)
  - [packages](http://golang.org/pkg)
  - [the tour](http://tour.golang.org)
  - [effective go article](http://golang.org/doc/effective_go.html)
  - [very good talk by Rob Pike](http://talks.golang.org/2012/splash.article)
  - [golang book](http://www.golang-book.com/)
- Bazil/fuse
  - ["fuse documentation"](http://libfuse.github.io/doxygen/)
  - [fuse function overview (for C)](https://www.cs.hmc.edu/~geoff/classes/hmc.cs135.201109/homework/fuse/fuse_doc.html#function-purposes)
  - [fuse options](http://manpages.ubuntu.com/manpages/xenial/man8/mount.fuse.8.html)
  - [MacFuse options](https://code.google.com/archive/p/macfuse/wikis/OPTIONS.wiki)
  - [Bazil](http://bazil.org/fuse/)
  - [talk](http://bazil.org/talks/2013-06-10-la-gophers/#1)


## What to do

You will expand `dfs.go` into a fully functional in-memory
file system. This means that files can be copied, created, edited, and
deleted on mount points exported by your code. 
The `dfs.go` file has most of the boilerplate.
You need to create objects that implement specific
interfaces (`fs.Node` and `fs.FS`), pass those to fuse, and
then fuse can call those objects' methods to implement file system
functionality. 
You will do this through definition of the following methods, probably using less
than 100 lines of code:
```
   func (FS) Root() (n fs.Node, err error)
   func (n *DFSNode) Attr(ctx context.Context, attr *fuse.Attr) error
   func (n *DFSNode) Lookup(ctx context.Context, name string) (fs.Node, error)
   func (n *DFSNode) ReadDirAll(ctx context.Context) ([]fuse.Dirent, error)
   func (n *DFSNode) Getattr(ctx context.Context, req *fuse.GetattrRequest, resp *fuse.GetattrResponse) error
   func (n *DFSNode) Fsync(ctx context.Context, req *fuse.FsyncRequest) error
   func (n *DFSNode) Setattr(ctx context.Context, req *fuse.SetattrRequest, resp *fuse.SetattrResponse) error
   func (p *DFSNode) Mkdir(ctx context.Context, req *fuse.MkdirRequest) (fs.Node, error)
   func (p *DFSNode) Create(ctx context.Context, req *fuse.CreateRequest, resp *fuse.CreateResponse) 
        (fs.Node, fs.Handle, error)
   func (n *DFSNode) ReadAll(ctx context.Context) ([]byte, error)
   func (n *DFSNode) Write(ctx context.Context, req *fuse.WriteRequest, resp *fuse.WriteResponse) error
   func (n *DFSNode) Flush(ctx context.Context, req *fuse.FlushRequest) error
   func (n *DFSNode) Remove(ctx context.Context, req *fuse.RemoveRequest) error
   func (n *DFSNode) Rename(ctx context.Context, req *fuse.RenameRequest, newDir fs.Node) error
```

Sadly, FUSE as a whole is documented extremely poorly, and the language
bindings (implemented by third parties) are no exception. bazil/fuse
is "documented" at
[http://godoc.org/bazil.org/fuse](http://godoc.org/bazil.org/fuse),
but the code itself is probably a better resource.
The best documentation will be in looking through the bazil/fuse code
that will be calling your code. In particular, the following two files
are most useful:
```
     bazil.org/fuse/fuse.go
        and
     bazil.org/fuse/fs/serve.go
```
both under `~/go/pkg/mod/ 
                      
### Modules
Before attempting to compile or run your program, you need to create
the dependencies for the p1 *module* (programs are part of packages,
packages are part of modules).

You presumably already executed the following two files in the
directory containing `dfs.go` as it also contains `hello.go`:
```
  go mod init
  go mod tidy
```
These commands do the following:
- derive versioned dependencies and explictly write them to text files
in the same directory
- fetch and build the dependencies if they do not already exist locally

You are now ready to try compiling and running the program.
You can run Go programs a few different ways; I tend to just use the
"go run dfs.go -d -m localdir" command as above when debugging. 

To deconstruct this
line, "go run" compiles/links/runs the program in `dfs.go`.
Flag "-d" causes `dfs.go` to print lots of debugging
information ("-fuse.debug" will cause the bazil/fuse shim layer to
print out even more debugging information). Though I advise you to
have debugging options, I will not test or use this. You must have
'-m'. I don't care if you have a default directory to mount your file
system on top of, but you must allow this directory to be changed via
the '-m' flag. '-m' should also *create* the mount point if it doesn't
already exist.

### Running and Debugging

A few last points:
- File systems are mounted onto directories in UNIX-like systems by
*mount*-ing them. However, this is taken care of automatically when
using the high-level Bazil interface `fs.Serve()`. 

- Unmounting is usually done like `sudo
  umount /tmp/dss`. However,
`dfs.go` includes a call to `fuse.Unmount()`, which
appears to work on both my macs and a linux. 
*Caveat:* The unmount will not work if the old mount is still
being used. For example, an unmount of `/tmp/dss` will fail if
the working directory of any shell is inside the mount point.

- Run `dfs`, kill w/
Ctrl-c, and accessing the directory might give you a "Device not
configured" error. However, running dfs again on the same mount point appears to correctly
unmount the directory and re-use it.
If this does not work, for some reason, the mountpoint will timeout and become useable again
after a few minutes anyway.
- You can add your own command-line arguments; see the
  implementation of the "-debug" flag in `dfs.go` as an example.
- You *may* attempt to debug with `gdb`, but `gdb`
has poor support for high-level Go structures like channels, maps, and
go-routines. That said, feel free to try:
[https://golang.org/doc/gdb](https://golang.org/doc/gdb). I
would use this inside of `xemacs` or `Aquamacs`. Let me
know if it works.
- Note that write calls might start at offsets *past* the current
size of the file (`gcc` can do this, for example). This is valid; just
initialize the space between the current size and the offset
with zeros.
- `remove` decrements the `Nlinks` attribute, removing the file entirely
if Nlinks reaches 0 (as it will).

## The details

You should have the following goals in developing your implementation:
- allowing file/directory creation, manipulation, deletion.
- allowing mounted files to be edited with emacs/vi (note:
  this might break for some unknown reason if you delete the
  `DFSNode.fsync()` method.).
- allowing a large C project to be copied and built on top of your system.

Non-goals:
- file permissions

## Testing
I should be able to make directories, copy files in, rename files, edit files using vi,
and compile [redis-4.0.11](https://gitlab.cs.umd.edu/keleher/cmsc818epublicf2021/-/blob/master/redis-4.0.11.tar.gz). You may get a few warnings, but the last few
lines should be:

```
    CC rax.o
    LINK redis-server
    INSTALL redis-sentinel
    CC redis-cli.o
    LINK redis-cli
    CC redis-benchmark.o
    LINK redis-benchmark
    INSTALL redis-check-rdb
    INSTALL redis-check-aof

Hint: It's a good idea to run 'make test' ;)
```

### Deliverables, and grading

Code should be formatted as per Go conventions. If in doubt, run "go
fmt" over your code. 

I will grade each project out of 100 points, with up to 20 points
coming from style. I do not care about comments per se, but I will
give the most points to implementations that are the cleanest,
simplest, and most efficient, probably in that order.

Your code should be entirely implemented inside the `dfs.go` skeleton
provided. To submit, *just push your files back to gitlab*.  This will
probably work w/ the following commands:
```
    git commit -a -m auto
    git push origin master
```

If you added files to this directory, you would first have to add them
with `git add file1....`, but you will not create any new files in
this project.

### Timeliness

All of the projects have due dates. You will lose 10 points for every
day your project is late. More importantly, note that the next project
will be released the day the previous project is due.

### Academic Integrity
You are all advanced students. Please act like it.

You *may* discuss the project with other students, but you should
not look at their code, nor share your own.

You *may* look at code and other resources online. However, if
your code ends up looking like code from the web, and another student
does the same, the situation is indistinguishable from cheating.

You *may* use the piazza website, and even post code. However,
**you may not** post any code for the projects. `You may`
post small snippets of generic Go illustrate or query some aspect of
the language.

### Zen
In general, I suggest the following approach for building projects in this class:

- *keep algorithms simple* - no need to write a complicated
  search procedure when a simple linear search will suffice (or a
  sophisticated search procedure is callable from the standard library). We are
  interested in concepts and functionality in this class, not
  polish. We will be building proof-of-concept file systems, not
  commercial products.
- *keep data representation simple*: Internal representations of files and directories is
  up to you.  The skeleton in `dfs.go` takes an extremely simple approach using a single
  structure (the `DFSNode`) to represent both.  You could use distinct structures, but it
  seems to ramp up the complexity.  

