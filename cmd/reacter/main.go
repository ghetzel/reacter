package main

import (
	"fmt"
	"io"
	"os"
	"time"

	"github.com/ghetzel/cli"
	"github.com/ghetzel/go-stockutil/log"
	"github.com/ghetzel/go-stockutil/stringutil"
	"github.com/ghetzel/reacter"
	"github.com/ghetzel/reacter/util"
)

const DefaultLogLevel = `notice`

func main() {
	app := cli.NewApp()
	app.Name = util.ApplicationName
	app.Usage = util.ApplicationSummary
	app.Version = util.ApplicationVersion

	app.Flags = []cli.Flag{
		cli.StringFlag{
			Name:   `log-level, L`,
			Usage:  `Level of log output verbosity`,
			Value:  DefaultLogLevel,
			EnvVar: `LOGLEVEL`,
		},
		cli.StringFlag{
			Name:   `node-name, n`,
			Usage:  `The name of the node to use when reporting check output`,
			EnvVar: `REACTER_ID`,
		},
		cli.StringFlag{
			Name:   `config-file, f`,
			Usage:  `Path to a unified YAML configuration file`,
			Value:  reacter.DefaultConfigFile,
			EnvVar: `REACTER_CONFIG`,
		},
		cli.StringFlag{
			Name:   `config-dir, c`,
			Usage:  `The directory containing YAML configuration files`,
			Value:  reacter.DefaultConfigDir,
			EnvVar: `REACTER_CONFIG_DIR`,
		},
		cli.StringFlag{
			Name:   `http-address, a`,
			Usage:  `If provided, start an HTTP server at this address and serve a web interface.`,
			EnvVar: `REACTER_HTTP`,
		},
		cli.StringFlag{
			Name:   `http-path-prefix`,
			Usage:  `If specified, frontend web assets will be expected to be served from this URL subdirectory.`,
			EnvVar: `REACTER_HTTP_PREFIX`,
		},
		cli.BoolFlag{
			Name:   `zeroconf`,
			Usage:  `Publish and perform automatic discovery of peer Reacter instances`,
			EnvVar: `REACTER_ZEROCONF`,
		},
		cli.StringFlag{
			Name:  `zeroconf-ec2-tag`,
			Usage: `If specified, a list of peers will be created by finding Amazon EC2 instances with the given tag. If specified as Tag=Value, the tag must match the given value.`,
		},
	}

	app.Before = func(c *cli.Context) error {
		log.SetLevelString(c.String(`log-level`))
		return nil
	}

	app.Action = func(c *cli.Context) {
		// wire up check outputs directly to handler inputs
		src, dst := io.Pipe()
		go runHandlers(c, src)
		runChecks(c, dst)
	}

	app.Commands = []cli.Command{
		{
			Name:  `check`,
			Usage: `Start performing checks on an interval and outputting the results`,
			Flags: []cli.Flag{
				cli.BoolFlag{
					Name:  `print-json, j`,
					Usage: `Print check events as JSON on standard output`,
				},
				cli.BoolFlag{
					Name:  `only-changes, C`,
					Usage: `Only emit events when the state is different than the previous one`,
				},
				cli.BoolFlag{
					Name:  `no-flapping, F`,
					Usage: `Do not emit events whose checks are flapping between okay and non-okay`,
				},
			},
			Action: func(c *cli.Context) {
				runChecks(c, nil)
			},
		}, {
			Name:  `handle`,
			Usage: `Receive check events and execute handlers`,
			Action: func(c *cli.Context) {
				runHandlers(c, os.Stdin)
			},
		}, {
			Name:  `cacher`,
			Usage: `Periodically execute handler queries and save their output to a cache directory`,
			Flags: []cli.Flag{
				cli.StringFlag{
					Name:  `cache-dir, C`,
					Usage: `The location of the directory to save cache output to`,
					Value: reacter.DefaultCacheDir,
				},
				cli.BoolFlag{
					Name:  `once, o`,
					Usage: `Regenerate the cache once and exit`,
				},
				cli.DurationFlag{
					Name:  `interval, I`,
					Usage: `How often the cache should be regenerated for each handler query command`,
					Value: (60 * time.Second),
				},
			},
			Action: func(c *cli.Context) {
				f := reacter.NewEventRouter()
				f.ConfigFile = c.GlobalString(`config-file`)
				f.ConfigDir = c.GlobalString(`config-dir`)

				f.CacheDir = c.String(`cache-dir`)
				intv := c.Duration(`interval`)

				if c.Bool(`once`) {
					intv = time.Duration(0)
				}

				if err := f.RunQueryCacher(intv); err != nil {
					log.Fatalf("%v", err)
				}
			},
		}, {
			Name:  `consume`,
			Usage: `Connect to an AMQP message broker and print check events to standard output`,
			Flags: []cli.Flag{
				cli.StringFlag{
					Name:  `queue, Q`,
					Usage: `The name of the queue to bind to`,
					Value: reacter.DEFAULT_QUEUE_NAME,
				},
				cli.BoolFlag{
					Name:  `durable, D`,
					Usage: `Durable queues will survive server restarts and remain when there are no remaining consumers or bindings`,
				},
				cli.BoolFlag{
					Name:  `autodelete, A`,
					Usage: `Auto-deleted queues will be automatically removed when all clients disconnect`,
				},
				cli.BoolFlag{
					Name:  `exclusive, E`,
					Usage: `Exclusive queues are only accessible by the connection that declares them and will be deleted when the connection closes`,
				},
			},
			Action: func(c *cli.Context) {
				if len(c.Args()) > 0 {
					if consumer, err := reacter.NewConsumer(c.Args()[0]); err == nil {
						if q := c.String(`queue`); q != `` {
							consumer.QueueName = q
						}

						consumer.Durable = c.Bool(`durable`)
						consumer.Autodelete = c.Bool(`autodelete`)
						consumer.Exclusive = c.Bool(`exclusive`)

						log.Infof("Connecting to %s:%d vhost=%s queue=%s", consumer.Host, consumer.Port, consumer.Vhost, consumer.QueueName)

						if err := consumer.Connect(); err == nil {
							if msgs, err := consumer.Subscribe(); err == nil {
								for msg := range msgs {
									fmt.Println(msg)
								}
							} else {
								log.Fatalf("Error subscribing: %v", err)
							}
						} else {
							log.Fatalf("Error connecting to consumer: %v", err)
						}
					} else {
						log.Fatalf("Error initializing consumer: %v", err)
					}
				} else {
					log.Fatalf("Must provide an AMQP connection URI as an argument")
				}
			},
		},
	}

	//  load plugin subcommands
	// app.Commands = append(app.Commands, api.Register()...)

	app.Run(os.Args)
}

func runChecks(c *cli.Context, dst io.Writer) {
	f := reacter.NewReacter()
	f.ConfigFile = c.GlobalString(`config-file`)
	f.ConfigDir = c.GlobalString(`config-dir`)

	if name := c.GlobalString(`node-name`); name == `` {
		if hostname, err := os.Hostname(); err == nil {
			f.NodeName = hostname
		} else {
			f.NodeName = stringutil.UUID().String()
		}
	} else {
		f.NodeName = name
	}

	log.Infof("Node name is '%s'", f.NodeName)

	f.PrintJson = c.Bool(`print-json`)
	f.WriteJson = dst
	f.OnlyPrintChanges = c.Bool(`only-changes`)
	f.SuppressFlapping = c.Bool(`no-flapping`)

	if addr := c.GlobalString(`http-address`); addr != `` {
		server := reacter.NewServer(f)
		server.PathPrefix = c.GlobalString(`http-path-prefix`)
		server.ZeroconfMDNS = c.GlobalBool(`zeroconf`)
		server.ZeroconfEC2Tag = c.GlobalString(`zeroconf-ec2-tag`)

		log.Infof("Starting HTTP server at %v", addr)
		go server.ListenAndServe(addr)
	}

	if err := f.Run(); err != nil {
		log.Fatalf("[checks] %v", err)
	}
}

func runHandlers(c *cli.Context, src io.Reader) {
	f := reacter.NewEventRouter()
	f.ConfigFile = c.GlobalString(`config-file`)
	f.ConfigDir = c.GlobalString(`config-dir`)

	if err := f.Run(src); err != nil {
		log.Fatalf("[handlers] %v", err)
	}
}
