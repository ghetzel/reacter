package main

import (
	"fmt"
	"os"
	"time"

	"github.com/codegangsta/cli"
	"github.com/ghetzel/go-stockutil/stringutil"
	"github.com/ghetzel/reacter"
	"github.com/ghetzel/reacter/util"
	"github.com/op/go-logging"
)

var log = logging.MustGetLogger(`main`)

const (
	DEFAULT_LOGLEVEL  = `info`
	DEFAULT_CONFIGDIR = `/opt/reacter/conf.d`
)

func main() {
	app := cli.NewApp()
	app.Name = util.ApplicationName
	app.Usage = util.ApplicationSummary
	app.Version = util.ApplicationVersion
	app.EnableBashCompletion = false
	app.Before = func(c *cli.Context) error {
		logging.SetFormatter(logging.MustStringFormatter(`%{color}%{level:.4s}%{color:reset}[%{id:04d}] %{message}`))

		if level, err := logging.LogLevel(c.String(`log-level`)); err == nil {
			logging.SetLevel(level, `main`)
			logging.SetLevel(level, `reacter`)
		}

		return nil
	}

	app.Flags = []cli.Flag{
		cli.StringFlag{
			Name:   `log-level, L`,
			Usage:  `Level of log output verbosity`,
			Value:  DEFAULT_LOGLEVEL,
			EnvVar: `LOGLEVEL`,
		},
		cli.StringFlag{
			Name:   `node-name, n`,
			Usage:  `The name of the node to use when reporting check output`,
			EnvVar: `ID`,
		},
		cli.StringFlag{
			Name:   `config-dir, c`,
			Usage:  `The directory containing YAML configuration files`,
			Value:  DEFAULT_CONFIGDIR,
			EnvVar: `CONFIG_DIR`,
		},
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
					Usage: `No not emit events whose checks are flapping between okay and non-okay`,
				},
			},
			Action: func(c *cli.Context) {
				f := reacter.NewReacter()

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
				f.OnlyPrintChanges = c.Bool(`only-changes`)
				f.SuppressFlapping = c.Bool(`no-flapping`)

				if err := f.LoadConfigDir(c.GlobalString(`config-dir`)); err == nil {
					if err := f.Run(); err != nil {
						log.Fatalf("%v", err)
					}
				} else {
					log.Fatalf("Failed to load configuration: %v", err)
				}
			},
		}, {
			Name:  `handle`,
			Usage: `Receive check events and execute handlers`,
			Action: func(c *cli.Context) {
				f := reacter.NewEventRouter()

				if err := f.LoadConfigDir(c.GlobalString(`config-dir`)); err == nil {
					if err := f.Run(os.Stdin); err != nil {
						log.Fatalf("%v", err)
					}
				} else {
					log.Fatalf("Failed to load configuration: %v", err)
				}
			},
		}, {
			Name:  `cacher`,
			Usage: `Periodically execute handler queries and save their output to a cache directory`,
			Flags: []cli.Flag{
				cli.StringFlag{
					Name:  `cache-dir, C`,
					Usage: `The location of the directory to save cache output to`,
					Value: reacter.DEFAULT_CACHE_DIR,
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

				if err := f.LoadConfigDir(c.GlobalString(`config-dir`)); err == nil {
					f.CacheDir = c.String(`cache-dir`)
					intv := c.Duration(`interval`)

					if c.Bool(`once`) {
						intv = time.Duration(0)
					}

					if err := f.RunQueryCacher(intv); err != nil {
						log.Fatalf("%v", err)
					}
				} else {
					log.Fatalf("Failed to load configuration: %v", err)
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
		}, {
			Name:  `version`,
			Usage: `Output the current version and exit`,
			Action: func(c *cli.Context) {
				fmt.Println(util.ApplicationVersion)
			},
		},
	}

	//  load plugin subcommands
	// app.Commands = append(app.Commands, api.Register()...)

	app.Run(os.Args)
}
