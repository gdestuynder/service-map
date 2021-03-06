// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.
//
// Contributor:
// - Aaron Meihm ameihm@mozilla.com

package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	slib "servicelib"
)

// Return SystemGroup for system group specified by sgid
func getSysGroup(op opContext, sgid string) (slib.SystemGroup, error) {
	var sg slib.SystemGroup

	err := op.QueryRow(`SELECT sysgroupid, name
		FROM sysgroup WHERE sysgroupid = $1`, sgid).Scan(&sg.ID, &sg.Name)
	if err != nil {
		if err == sql.ErrNoRows {
			return sg, nil
		} else {
			return sg, err
		}
	}
	return sg, nil
}

// Add metadata do the system group, including host details
func sysGroupAddMeta(op opContext, s *slib.SystemGroup) error {
	s.Host = make([]slib.Host, 0)

	// Grab any hosts that have been mapped to this group.
	rows, err := op.Query(`SELECT assetid, hostname, comment, lastused,
		sysgroupid, dynamic
		FROM asset WHERE sysgroupid = $1
		AND assettype = 'host'`, s.ID)
	if err != nil {
		return err
	}
	for rows.Next() {
		var h slib.Host
		err = rows.Scan(&h.ID, &h.Hostname, &h.Comment, &h.LastUsed,
			&h.SysGroupID, &h.Dynamic)
		if err != nil {
			return err
		}
		err = hostAddComp(op, &h)
		if err != nil {
			return err
		}
		err = hostAddVuln(op, &h)
		if err != nil {
			return err
		}
		s.Host = append(s.Host, h)
	}

	// Grab any sites that have been mapped to this group.
	rows, err = op.Query(`SELECT assetid, website, comment, lastused,
		sysgroupid, dynamic
		FROM asset WHERE sysgroupid = $1
		AND assettype = 'website'`, s.ID)
	if err != nil {
		return err
	}
	for rows.Next() {
		var h slib.Website
		err = rows.Scan(&h.ID, &h.Website, &h.Comment, &h.LastUsed,
			&h.SysGroupID, &h.Dynamic)
		if err != nil {
			return err
		}
		err = siteAddHTTPObs(op, &h)
		if err != nil {
			return err
		}
		s.Website = append(s.Website, h)
	}

	return nil
}

// API entry point to retrieve a given system group
func serviceGetSysGroup(rw http.ResponseWriter, req *http.Request) {
	req.ParseForm()

	sgid := req.FormValue("id")
	if sgid == "" {
		http.Error(rw, "must specify a valid system group id", 500)
		return
	}

	op := opContext{}
	op.newContext(dbconn, false, req.RemoteAddr)

	sg, err := getSysGroup(op, sgid)
	if err != nil {
		op.logf(err.Error())
		http.Error(rw, err.Error(), 500)
		return
	}
	if sg.Name == "" {
		http.NotFound(rw, req)
		return
	}
	err = sysGroupAddMeta(op, &sg)
	if err != nil {
		op.logf(err.Error())
		http.Error(rw, err.Error(), 500)
		return
	}

	buf, err := json.Marshal(&sg)
	if err != nil {
		op.logf(err.Error())
		http.Error(rw, err.Error(), 500)
		return
	}
	fmt.Fprintf(rw, string(buf))
}

// API entry point to retrieve all system groups
func serviceSysGroups(rw http.ResponseWriter, req *http.Request) {
	op := opContext{}
	op.newContext(dbconn, false, req.RemoteAddr)

	rows, err := op.db.Query(`SELECT sysgroupid FROM sysgroup`)
	if err != nil {
		op.logf(err.Error())
		http.Error(rw, err.Error(), 500)
		return
	}
	sgr := slib.SystemGroupsResponse{}
	sgr.Results = make([]slib.SystemGroup, 0)
	for rows.Next() {
		var sgid string
		err = rows.Scan(&sgid)
		if err != nil {
			rows.Close()
			op.logf(err.Error())
			http.Error(rw, err.Error(), 500)
			return
		}
		sg, err := getSysGroup(op, sgid)
		if err != nil {
			rows.Close()
			op.logf(err.Error())
			http.Error(rw, err.Error(), 500)
			return
		}
		if sg.Name == "" {
			continue
		}
		sgr.Results = append(sgr.Results, sg)
	}
	err = rows.Err()
	if err != nil {
		op.logf(err.Error())
		http.Error(rw, err.Error(), 500)
		return
	}

	buf, err := json.Marshal(&sgr)
	if err != nil {
		op.logf(err.Error())
		http.Error(rw, err.Error(), 500)
		return
	}
	fmt.Fprint(rw, string(buf))
}
