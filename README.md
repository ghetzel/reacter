# Reacter
A tool for generating, consuming, and handling system monitoring events

## Overview

1. Executes Nagios-compatible check scripts
2. Collects output and prints it as a formatted JSON string to standard output
3. Publish output to an AMQP message broker
4. Consumes output from an AMQP message broker
5. Conditionally executes handler scripts based on check details and handler criteria

## Checks: `reacter check`
Check scripts are consistent with the [Nagios Plugin API](https://assets.nagios.com/downloads/nagioscore/docs/nagioscore/3/en/pluginapi.html).  Checks can be any shell-executable program that exits with status 0 (OK), 1 (Warning), 2 (Critical), or 3+ (Unknown).  Plugin output and performance data is parsed from the check's standard output.

### Configuration
Checks are configured via a YAML file placed in a directory that Reacter will load the definitions from (specified via the `--config-dir` flag.)  An example check definition looks like the following:

```yaml
---
checks:
- name:                'my_cool_check'
  command:             ['my_cool_check', '--warning', '10', '--critical', '5']
  directory:           '/usr/local/bin'
  interval:            30
  timeout:             5000
  fall:                3
  rise:                2
  flap_threshold_high: 0.35
  flap_threshold_low:  0.15
  environment:
    HOME:      '/srv/home'
    OTHER_VAR: 6
    IS_COOL:   true
```

The configuration consists of a top-level `checks` array populated with one or more check definitions.  Check definition fields are:

| Field                 | Type             | Required | Default  | Description
| --------------------- | ---------------- | -------- | -------- | -----------
| `name`                | String           | Yes      |          | The name of the check
| `command`             | Array(String)    | Yes      |          | The command expressed as an array of command and command-line parameters
| `directory`           | String           | No       | `$(pwd)` | The working directory to use when executing the command
| `interval`            | Integer          | No       | 60       | How often (in seconds) to execute the check
| `timeout`             | Integer          | No       | 3000     | The timeout (in milliseconds) before killing the check if it hasn't finished
| `fall`                | Integer          | No       | 1        | How many checks need to fail before reporting the change in status
| `rise`                | Integer          | No       | 1        | How many checks need to succeed after failing before reporting okay
| `environment`         | Hash(String,Any) | No       |          | A hash of key-value pairs that will be passed to the command as environment variables; replaces the calling shell environment
| `flap_threshold_high` | Float            | No       | 0.5      | Maximum instability a service needs to be (0.0-1.0) to start flapping
| `flap_threshold_low`  | Float            | No       | 0.25     | How unstable a service needs to be (0.0-1.0) to stop flapping


### Publication
Check results can be emitted to standard output for consumption by the `reacter handler` invocation of this utility, or by another service/program.  One of the intended use cases is to emit results an HTTP POST them to a web service which will enqueue the messages to an AMQP message broker for later consumption by handlers.

The output format of a check is as follows:

```json
{
  "check":{
    "node_name":"myhost",
    "name":"my_cool_check",
    "command":["my_cool_check", "--warning", "10", "--critical", "5"],
    "timeout": 5000,
    "enabled":true,
    "state": 0,
    "hard":  true,
    "changed": true,
    "interval": 30,
    "rise": 3,
    "fall": 2,
    "observations": {
      "size": 21,
      "flapping": false,
      "flap_detection": true,
      "flap_threshold_low": 0.15,
      "flap_threshold_high":0.35,
      "flap_factor": 0
    }
  },
  "output": "OK",
  "error": false,
  "timestamp":"1970-01-01T12:59:00.000000000-04:00"
}
```

This is formatted to be readable, but is output from `reacter check` as a single line, each line representing the output from one check's execution.  Some of the fields in the output are described below.

| Field                      | Type             | Description
| -----------------------    | ---------------- | -----------
| `check.node_name`          | String           | The hostname of the host the check executed on, or the value of `--node-name`
| `check.enabled`            | Boolean          | Whether the check is enabled or not
| `check.state`              | Integer          | The exit status of the check script
| `check.hard`               | Boolean          | If a check is in the process of rising or falling, the status will remain unchanged but this field will be `false`
| `check.changed`            | Boolean          | If the previous state of a check is different from the current state, this field will be `true`
| `error`                    | Boolean          | If the check script experienced an error that prevented execution, this will be `true`
| `observations.flapping`    | Boolean          | If the check is oscillating between an okay and non-okay state, this will be `true`
| `observations.size`        | Integer          | How many of the most-recent check states are stored in memory for flap detection
| `observations.flap_factor` | Float            | The current flap factor, which is compared to the high/low thresholds to determine if the check if flapping
| `output`                   | String           | The standard output captured from the check script's execution


## Handlers: `reacter handle`
Handlers are executed in response to check results read from standard input.  The handler definitions define the conditions on which a handler will be executed.  The conditions include factors such as node name, check name, state, whether the check is flapping, and whether the check has changed state.  Using these conditions, handlers can be executed for only a subset of check results as they stream in.  Multiple handlers can respond to the same result, as each result is evaluated against each handler definition as it is processed.

### Configuration
Handlers, like checks, are configured via a YAML file placed in a directory that Reacter will load the definitions from (specified via the `--config-dir` flag.)  An example handler definition looks like the following:

```yaml
---
handlers:
- name:                'my_team_slack_chat'
  command:             ['reacter-slack']
  timeout:             6000
  directory:           '/usr/local/bin'
  query:               ['bash', '-c', 'get_my_nodes > /tmp/node-list.txt']
  query_timeout:       3000
  nodefile:            /tmp/node-list.txt

  node_names:
  - my_node1
  - my_node2

  checks:
  - my_cool_check

  flapping: false
  only_changes: true

  parameters:
    token:   abc123def456
    channel: my-channel

  environment:
    HOME:      '/srv/home'

```

The configuration consists of a top-level `handlers` array populated with one or more handler definitions.  Handler definition fields are:

| Field                 | Type             | Required | Default  | Description
| --------------------- | ---------------- | -------- | -------- | -----------
| `name`                | String           | Yes      |          | The name of the handler
| `command`             | Array(String)    | Yes      |          | The handler command expressed as an array of command and command-line parameters
| `directory`           | String           | No       | `$(pwd)` | The working directory to use when executing the command
| `query`               | Array(String)    | No       |          | A command to execute before the handler that will return a list of nodes to respond to
| `query_timeout`       | Integer          | No       | 3000     | How long to wait for the query command to execute before killing it
| `nodefile`            | String           | No       |          | A path to a file containing a list of nodes to respond to
| `node_names`          | Array(String)    | No       |          | A list of nodes to respond to (will override `query` and `nodefile`)
| `checks`              | Array(String)    | No       |          | A list of check names to respond to
| `flapping`            | Boolean          | No       | true     | Whether to handle flapping checks or not
| `only_changes`        | Boolean          | No       | false    | Whether to only handle state changes or not (uses the check result `changed` field)
| `parameters`          | Hash(String,Any) | No       |          | A hash of key-value pairs to pass to the handler command as environment variables; prefixed with `REACTER_PARAM_`
| `environment`         | Hash(String,Any) | No       |          | A hash of key-value pairs to pass to the handler command as environment variables; replaces the calling shell environment

### Handler Scripts
Handler scripts are executed only when a handler definition's conditions are met.  These scripts can be built to do anything that you need done to respond to a check result.  This typically includes things like sending a PagerDuty alert, posting a notification to a Slack channel, or forwarding check data to a time series database.  Handler scripts are called with several well-know environment variables that the handler may use to provide context-specific details about the check result being handled.  These variables include:

| Environment Variable   | Description
| ---------------------- | -----------
| REACTER_CHECK_ID       | The check's node name concatenated with the check name, joined by a `:`. This can be used to uniquely identify a check from a specific node for services that require stateful information to clear events after they are first generated.
| REACTER_CHECK_NAME     | The name of the check being handled
| REACTER_CHECK_NODE     | The node name that the check was emitted from (corresponds to `--node-name` from `reacter check`)
| REACTER_EPOCH          | The epoch time of the check event (seconds since Jan 1 1970)
| REACTER_EPOCH_MS       | The epoch time of the check event (milliseconds since Jan 1 1970)
| REACTER_HANDLER        | The name of the handler as defined in the handler definition configuration
| REACTER_STATE          | The state of the check result being handled; one of "okay", "warning", "critical", or "unknown"
| REACTER_STATE_CHANGED  | `0` if the state is unchanged, `1` if the check's state has changed
| REACTER_STATE_FLAPPING | `0` if the check is not flapping, `1` if it is
| REACTER_STATE_HARD     | `0` if the check is rising or falling, `1` if the check is in a hard state
| REACTER_STATE_ID       | The numeric exit status of the check result that was emitted from the check script
| REACTER_PARAM_*        | Expanded to include any parameters specified in the `parameters` hash for the handler definition. All keys are converted to uppercase.

### Node Queries and Caching Features
