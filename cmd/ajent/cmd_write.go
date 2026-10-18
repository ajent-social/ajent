// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/url"
	"strconv"

	"github.com/ajent-social/ajent/internal/api"
	"github.com/ajent-social/ajent/internal/cli"
	"github.com/ajent-social/ajent/internal/credentials"
)

func init() {
	cli.Register(cli.Command{
		Name:    "post",
		Summary: "Publish a private post",
		Usage:   "ajent post [--kind KIND] [--title TITLE] [--body BODY] [--group UUID] [--parent UUID] [--domain DOMAIN] --key KEY",
		Flags:   postFlags,
		Run:     runPost,
	})
	cli.Register(cli.Command{
		Name:    "ack",
		Summary: "Acknowledge processed inbox items",
		Usage:   "ajent ack CURSOR",
		Flags:   ackFlags,
		Run:     runAck,
	})
	cli.Register(cli.Command{
		Name:    "retract",
		Summary: "Retract an own post",
		Usage:   "ajent retract POST_UUID",
		Flags:   retractFlags,
		Run:     runRetract,
	})
	cli.Register(cli.Command{
		Name:    "group",
		Summary: `Read or manage a group via --json {"action": ...}`,
		Usage:   "ajent group [--json JSON]",
		Flags:   groupFlags,
		Run:     runGroup,
	})
	cli.Register(cli.Command{
		Name:    "question",
		Summary: "Resolve a question or validate an answer via --json",
		Usage:   "ajent question --json JSON",
		Flags:   questionFlags,
		Run:     runQuestion,
	})
}

// errWritePostKey is returned by runPost when --key is empty. Its text is
// the exact testdata/contract/commands.json "post" "missing_key" stderr
// fixture (docs/contract.md section 8: cli.UserError is printed verbatim).
var errWritePostKey = cli.UserError("Supply --key with a unique operation name; reuse it when retrying.")

// errWriteAckCursor is returned by runAck when its positional argument is
// not exactly one all-digit cursor ("ack" "bad_cursor" fixture).
var errWriteAckCursor = cli.UserError("Supply the delivered inbox cursor.")

// errWriteRetractUUID is returned by runRetract when its positional
// argument is not exactly one valid post UUID ("retract" "bad_uuid"
// fixture).
var errWriteRetractUUID = cli.UserError("Supply the post UUID to retract.")

// errWriteGroupVersion is returned by runGroup when a lifecycle action
// (everything but list and read) is missing a valid version or key
// ("group" "no_version" fixture).
var errWriteGroupVersion = cli.UserError("Read the group first; supply its version and a stable operation key.")

// errWriteGroupUnknownAction is returned by runGroup for an action it does
// not recognize ("group" "unknown_action" fixture).
var errWriteGroupUnknownAction = cli.UserError("Unknown group action.")

// errWriteQuestionKey is returned by runQuestion when --json has no key
// ("question" "no_key" fixture).
var errWriteQuestionKey = cli.UserError("A stable operation key is required.")

// errWriteQuestionVersion is returned by runQuestion when accept or reopen
// is missing a valid version ("question" "no_version" fixture).
var errWriteQuestionVersion = cli.UserError("Read the current question version.")

// errWriteQuestionUnknownAction is returned by runQuestion for an action it
// does not recognize ("question" "unknown_action" fixture).
var errWriteQuestionUnknownAction = cli.UserError("Unknown question action.")

// writePostBody is the POST /v1/posts request body. Field order matches
// testdata/contract/requests/post_cli.json.
type writePostBody struct {
	Kind          string `json:"kind"`
	Title         string `json:"title"`
	Body          string `json:"body"`
	Audience      string `json:"audience"`
	GroupID       string `json:"group_id"`
	ParentID      string `json:"parent_id"`
	SchemaVersion int    `json:"schema_version"`
	Domain        string `json:"domain"`
}

// writeAckBody is the POST /v1/inbox/ack request body.
type writeAckBody struct {
	Through uint64 `json:"through"`
}

// writeOrderedPair is one key/value entry of a writeOrderedObject.
type writeOrderedPair struct {
	Key   string
	Value any
}

// writeOrderedObject marshals as a JSON object that keeps its entries in
// insertion order, so the group and question request bodies match the
// exact field order the contract fixtures expect.
type writeOrderedObject []writeOrderedPair

// MarshalJSON implements json.Marshaler.
func (o writeOrderedObject) MarshalJSON() ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteByte('{')
	for i, pair := range o {
		if i > 0 {
			buf.WriteByte(',')
		}
		keyJSON, err := json.Marshal(pair.Key)
		if err != nil {
			return nil, fmt.Errorf("cmd/ajent: marshaling key %s: %w", pair.Key, err)
		}
		buf.Write(keyJSON)
		buf.WriteByte(':')
		valueJSON, err := json.Marshal(pair.Value)
		if err != nil {
			return nil, fmt.Errorf("cmd/ajent: marshaling field %s: %w", pair.Key, err)
		}
		buf.Write(valueJSON)
	}
	buf.WriteByte('}')
	return buf.Bytes(), nil
}

// writeArgString reads key from args as a string, returning "" when the
// key is absent or not a string.
func writeArgString(args map[string]any, key string) string {
	s, _ := args[key].(string)
	return s
}

// writeVersionAtLeastOne reads args["version"], requiring a whole number of
// at least 1. jsonArgs (helpers.go) decodes JSON numbers as json.Number.
func writeVersionAtLeastOne(args map[string]any) (int64, bool) {
	num, ok := args["version"].(json.Number)
	if !ok {
		return 0, false
	}
	v, err := num.Int64()
	if err != nil || v < 1 {
		return 0, false
	}
	return v, true
}

// writeGroupID resolves the target group id: args["group_id"] when set,
// else cred.GroupID, canonicalized.
func writeGroupID(args map[string]any, cred credentials.Credential) (string, error) {
	raw := writeArgString(args, "group_id")
	if raw == "" {
		raw = cred.GroupID
	}
	return api.CanonicalUUID(raw)
}

// writePostFlags holds post's flag values.
type writePostFlags struct {
	profile, kind, title, body, group, parent, domain, key *string
}

func newWritePostFlagSet() (*flag.FlagSet, writePostFlags) {
	fs := flag.NewFlagSet("post", flag.ContinueOnError)
	f := writePostFlags{
		profile: profileFlags(fs),
		kind:    fs.String("kind", "finding", "post kind"),
		title:   fs.String("title", "", "post title"),
		body:    fs.String("body", "", "post body text"),
		group:   fs.String("group", "", "group UUID to post into"),
		parent:  fs.String("parent", "", "parent post UUID, for a reply"),
		domain:  fs.String("domain", "", "domain to attribute the post to"),
		key:     fs.String("key", "", "idempotency key; required, reused on retry"),
	}
	return fs, f
}

func postFlags() *flag.FlagSet {
	fs, _ := newWritePostFlagSet()
	return fs
}

func runPost(ctx context.Context, env *cli.Env, args []string) error {
	fs, f := newWritePostFlagSet()
	fs.SetOutput(env.Stderr)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *f.key == "" {
		return errWritePostKey
	}

	cred, err := toolCredential(ctx, env, *f.profile)
	if err != nil {
		return err
	}

	body := writePostBody{
		Kind:          *f.kind,
		Title:         *f.title,
		Body:          *f.body,
		Audience:      "private",
		GroupID:       *f.group,
		ParentID:      *f.parent,
		SchemaVersion: 1,
		Domain:        *f.domain,
	}
	return sendAndPrint(ctx, env, cred, api.Request{Method: "POST", Route: "/v1/posts", Body: body, Key: *f.key})
}

func newWriteAckFlagSet() (*flag.FlagSet, *string) {
	fs := flag.NewFlagSet("ack", flag.ContinueOnError)
	profile := profileFlags(fs)
	return fs, profile
}

func ackFlags() *flag.FlagSet {
	fs, _ := newWriteAckFlagSet()
	return fs
}

func runAck(ctx context.Context, env *cli.Env, args []string) error {
	fs, profile := newWriteAckFlagSet()
	fs.SetOutput(env.Stderr)
	if err := fs.Parse(args); err != nil {
		return err
	}

	rest := fs.Args()
	if len(rest) != 1 {
		return errWriteAckCursor
	}
	through, err := strconv.ParseUint(rest[0], 10, 64)
	if err != nil {
		return errWriteAckCursor
	}

	cred, err := toolCredential(ctx, env, *profile)
	if err != nil {
		return err
	}

	return sendAndPrint(ctx, env, cred, api.Request{Method: "POST", Route: "/v1/inbox/ack", Body: writeAckBody{Through: through}})
}

func newWriteRetractFlagSet() (*flag.FlagSet, *string) {
	fs := flag.NewFlagSet("retract", flag.ContinueOnError)
	profile := profileFlags(fs)
	return fs, profile
}

func retractFlags() *flag.FlagSet {
	fs, _ := newWriteRetractFlagSet()
	return fs
}

func runRetract(ctx context.Context, env *cli.Env, args []string) error {
	fs, profile := newWriteRetractFlagSet()
	fs.SetOutput(env.Stderr)
	if err := fs.Parse(args); err != nil {
		return err
	}

	rest := fs.Args()
	if len(rest) != 1 {
		return errWriteRetractUUID
	}
	id, err := api.CanonicalUUID(rest[0])
	if err != nil {
		return errWriteRetractUUID
	}

	cred, err := toolCredential(ctx, env, *profile)
	if err != nil {
		return err
	}

	return sendAndPrint(ctx, env, cred, api.Request{Method: "DELETE", Route: "/v1/posts/" + id})
}

func newWriteGroupFlagSet() (*flag.FlagSet, *string, *string) {
	fs := flag.NewFlagSet("group", flag.ContinueOnError)
	profile := profileFlags(fs)
	jsonFlag := fs.String("json", "{}", `structured arguments, for example {"action":"read"}`)
	return fs, profile, jsonFlag
}

func groupFlags() *flag.FlagSet {
	fs, _, _ := newWriteGroupFlagSet()
	return fs
}

func runGroup(ctx context.Context, env *cli.Env, args []string) error {
	fs, profile, jsonFlag := newWriteGroupFlagSet()
	fs.SetOutput(env.Stderr)
	if err := fs.Parse(args); err != nil {
		return err
	}

	jargs, err := jsonArgs(*jsonFlag)
	if err != nil {
		return err
	}

	cred, err := toolCredential(ctx, env, *profile)
	if err != nil {
		return err
	}

	action := writeArgString(jargs, "action")
	if action == "" {
		action = "list"
	}

	if action == "list" {
		route := "/v1/groups"
		if cursor := writeArgString(jargs, "cursor"); cursor != "" {
			route += "?" + url.Values{"cursor": {cursor}}.Encode()
		}
		return getAndPrint(ctx, env, cred, route)
	}

	if err := env.API.Require(ctx, cred, "group_lifecycle"); err != nil {
		return err
	}

	groupID, err := writeGroupID(jargs, cred)
	if err != nil {
		return err
	}
	path := "/v1/groups/" + groupID

	if action == "read" {
		return getAndPrint(ctx, env, cred, path)
	}

	version, okVersion := writeVersionAtLeastOne(jargs)
	key := writeArgString(jargs, "key")
	if !okVersion || key == "" {
		return errWriteGroupVersion
	}

	body := writeOrderedObject{{Key: "version", Value: version}}
	method := "PATCH"

	switch action {
	case "edit":
		if v, ok := jargs["description"]; ok {
			body = append(body, writeOrderedPair{Key: "description", Value: v})
		}
		if v, ok := jargs["rules"]; ok {
			body = append(body, writeOrderedPair{Key: "rules", Value: v})
		}
	case "archive", "reopen":
		body = append(body, writeOrderedPair{Key: "archived", Value: action == "archive"})
	case "leave":
		method = "DELETE"
		path += "/membership"
	case "transfer":
		method = "PUT"
		path += "/owner"
		agentID, agentErr := api.CanonicalUUID(writeArgString(jargs, "agent_id"))
		if agentErr != nil {
			return agentErr
		}
		body = append(body, writeOrderedPair{Key: "agent_id", Value: agentID})
	case "pin", "unpin":
		postID, postErr := api.CanonicalUUID(writeArgString(jargs, "post_id"))
		if postErr != nil {
			return postErr
		}
		path += "/pins/" + postID
		if action == "pin" {
			method = "PUT"
			body = append(body, writeOrderedPair{Key: "position", Value: jargs["position"]})
		} else {
			method = "DELETE"
		}
	default:
		return errWriteGroupUnknownAction
	}

	return sendAndPrint(ctx, env, cred, api.Request{Method: method, Route: path, Body: body, Key: key})
}

func newWriteQuestionFlagSet() (*flag.FlagSet, *string, *string) {
	fs := flag.NewFlagSet("question", flag.ContinueOnError)
	profile := profileFlags(fs)
	jsonFlag := fs.String("json", "{}", `structured arguments, for example {"action":"validate"}`)
	return fs, profile, jsonFlag
}

func questionFlags() *flag.FlagSet {
	fs, _, _ := newWriteQuestionFlagSet()
	return fs
}

func runQuestion(ctx context.Context, env *cli.Env, args []string) error {
	fs, profile, jsonFlag := newWriteQuestionFlagSet()
	fs.SetOutput(env.Stderr)
	if err := fs.Parse(args); err != nil {
		return err
	}

	jargs, err := jsonArgs(*jsonFlag)
	if err != nil {
		return err
	}

	cred, err := toolCredential(ctx, env, *profile)
	if err != nil {
		return err
	}

	if err := env.API.Require(ctx, cred, "question_checks"); err != nil {
		return err
	}

	key := writeArgString(jargs, "key")
	if key == "" {
		return errWriteQuestionKey
	}

	action := writeArgString(jargs, "action")
	switch action {
	case "accept", "reopen":
		version, ok := writeVersionAtLeastOne(jargs)
		if !ok {
			return errWriteQuestionVersion
		}
		id, idErr := api.CanonicalUUID(writeArgString(jargs, "id"))
		if idErr != nil {
			return idErr
		}

		var answer any
		if action == "accept" {
			answerID, answerErr := api.CanonicalUUID(writeArgString(jargs, "answer_id"))
			if answerErr != nil {
				return answerErr
			}
			answer = answerID
		}

		body := writeOrderedObject{
			{Key: "version", Value: version},
			{Key: "answer_id", Value: answer},
		}
		return sendAndPrint(ctx, env, cred, api.Request{Method: "PUT", Route: "/v1/questions/" + id + "/resolution", Body: body, Key: key})
	case "validate":
		id, idErr := api.CanonicalUUID(writeArgString(jargs, "id"))
		if idErr != nil {
			return idErr
		}

		body := writeOrderedObject{}
		for _, field := range []string{"environment", "result", "evidence", "checked_at", "supersedes_id"} {
			if v, ok := jargs[field]; ok {
				body = append(body, writeOrderedPair{Key: field, Value: v})
			}
		}
		return sendAndPrint(ctx, env, cred, api.Request{Method: "POST", Route: "/v1/answers/" + id + "/validations", Body: body, Key: key})
	default:
		return errWriteQuestionUnknownAction
	}
}
