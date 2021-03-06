package commands

import (
	"context"
	"fmt"
	"io"
	"os"

	cmdenv "github.com/ipfs/go-ipfs/core/commands/cmdenv"
	"github.com/ipfs/go-ipfs/core/coreapi/interface"

	"gx/ipfs/QmSP88ryZkHSRn1fnngAaV2Vcn63WUJzAavnRM9CVdU1Ky/go-ipfs-cmdkit"
	cmds "gx/ipfs/QmXTmUCBtDUrzDYVzASogLiNph7EBuYqEgPL7QoHNMzUnz/go-ipfs-cmds"
)

const progressBarMinSize = 1024 * 1024 * 8 // show progress bar for outputs > 8MiB

var CatCmd = &cmds.Command{
	Helptext: cmdkit.HelpText{
		Tagline:          "Show IPFS object data.",
		ShortDescription: "Displays the data contained by an IPFS or IPNS object(s) at the given path.",
	},

	Arguments: []cmdkit.Argument{
		cmdkit.StringArg("ipfs-path", true, true, "The path to the IPFS object(s) to be outputted.").EnableStdin(),
	},
	Options: []cmdkit.Option{
		cmdkit.IntOption("offset", "o", "Byte offset to begin reading from."),
		cmdkit.IntOption("length", "l", "Maximum number of bytes to read."),
	},
	Run: func(req *cmds.Request, res cmds.ResponseEmitter, env cmds.Environment) error {
		node, err := cmdenv.GetNode(env)
		if err != nil {
			return err
		}

		api, err := cmdenv.GetApi(env)
		if err != nil {
			return err
		}

		if !node.OnlineMode() {
			if err := node.SetupOfflineRouting(); err != nil {
				return err
			}
		}

		offset, _ := req.Options["offset"].(int)
		if offset < 0 {
			return fmt.Errorf("cannot specify negative offset")
		}

		max, found := req.Options["length"].(int)
		if err != nil {
			return err
		}
		if max < 0 {
			return fmt.Errorf("cannot specify negative length")
		}
		if !found {
			max = -1
		}

		err = req.ParseBodyArgs()
		if err != nil {
			return err
		}

		readers, length, err := cat(req.Context, api, req.Arguments, int64(offset), int64(max))
		if err != nil {
			return err
		}

		/*
			if err := corerepo.ConditionalGC(req.Context, node, length); err != nil {
				re.SetError(err, cmdkit.ErrNormal)
				return
			}
		*/

		res.SetLength(length)
		reader := io.MultiReader(readers...)

		// Since the reader returns the error that a block is missing, and that error is
		// returned from io.Copy inside Emit, we need to take Emit errors and send
		// them to the client. Usually we don't do that because it means the connection
		// is broken or we supplied an illegal argument etc.
		return res.Emit(reader)
	},
	PostRun: cmds.PostRunMap{
		cmds.CLI: func(res cmds.Response, re cmds.ResponseEmitter) error {
			if res.Length() > 0 && res.Length() < progressBarMinSize {
				return cmds.Copy(re, res)
			}

			for {
				v, err := res.Next()
				if err != nil {
					if err == io.EOF {
						return nil
					}
					return err
				}

				switch val := v.(type) {
				case io.Reader:
					bar, reader := progressBarForReader(os.Stderr, val, int64(res.Length()))
					bar.Start()

					err = re.Emit(reader)
					if err != nil {
						return err
					}
				default:
					log.Warningf("cat postrun: received unexpected type %T", val)
				}
			}
		},
	},
}

func cat(ctx context.Context, api iface.CoreAPI, paths []string, offset int64, max int64) ([]io.Reader, uint64, error) {
	readers := make([]io.Reader, 0, len(paths))
	length := uint64(0)
	if max == 0 {
		return nil, 0, nil
	}
	for _, p := range paths {
		fpath, err := iface.ParsePath(p)
		if err != nil {
			return nil, 0, err
		}

		read, err := api.Unixfs().Cat(ctx, fpath)
		if err != nil {
			return nil, 0, err
		}
		if offset > int64(read.Size()) {
			offset = offset - int64(read.Size())
			continue
		}
		count, err := read.Seek(offset, io.SeekStart)
		if err != nil {
			return nil, 0, err
		}
		offset = 0

		size := uint64(read.Size() - uint64(count))
		length += size
		if max > 0 && length >= uint64(max) {
			var r io.Reader = read
			if overshoot := int64(length - uint64(max)); overshoot != 0 {
				r = io.LimitReader(read, int64(size)-overshoot)
				length = uint64(max)
			}
			readers = append(readers, r)
			break
		}
		readers = append(readers, read)
	}
	return readers, length, nil
}
