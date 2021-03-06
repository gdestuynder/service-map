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
	"github.com/lib/pq"
	"github.com/montanaflynn/stats"
	slib "servicelib"
	"strconv"
	"time"
)

// Calculate a risk scenario, uses compliance data as probability metric
func riskComplianceScenario(op opContext, rs *slib.RRAServiceRisk,
	src slib.RRAAttribute, desc string) error {
	// Calculate our step value based on the number of compliance checks
	// that have been executed for supporting hosts
	//
	// XXX We should filter compliance checks here that do not make sense
	// for a the service (e.g., filter out MAXIMUM related checks for low
	// requirement services, but this information isn't really captured
	// anywhere right now
	totalcnt := 0
	coverage := "complete"
	for _, x := range rs.RRA.SupportGrps {
		for _, y := range x.Host {
			var inc int
			inc += y.CompStatus.HighFail
			inc += y.CompStatus.HighPass
			inc += y.CompStatus.MediumFail
			inc += y.CompStatus.MediumPass
			inc += y.CompStatus.LowFail
			inc += y.CompStatus.LowPass
			totalcnt += inc

			// See if a host reported nothing, if so downgrade the
			// coverage
			if inc == 0 {
				coverage = "partial"
			}
		}
	}
	// If totalcnt is zero, we didn't have any data points.
	if totalcnt == 0 {
		ndp := slib.RiskScenario{
			Name:     "Compliance scenario for " + desc,
			NoData:   true,
			Coverage: "none",
		}
		rs.Scenarios = append(rs.Scenarios, ndp)
		return nil
	}
	stepv := 3.0 / float64(totalcnt)
	scr := 1.0
	for _, x := range rs.RRA.SupportGrps {
		for _, y := range x.Host {
			scr += stepv * float64(y.CompStatus.HighFail)
			scr += stepv * float64(y.CompStatus.MediumFail)
			scr += stepv * float64(y.CompStatus.LowFail)
		}
	}

	newscen := slib.RiskScenario{
		Name:        "Compliance scenario for " + desc,
		Impact:      src.Impact,
		Probability: scr,
		Score:       src.Impact * scr,
		Coverage:    coverage,
		NoData:      false,
	}
	err := newscen.Validate()
	if err != nil {
		return err
	}
	rs.Scenarios = append(rs.Scenarios, newscen)

	return nil
}

func riskVulnerabilityScenario(op opContext, rs *slib.RRAServiceRisk,
	src slib.RRAAttribute, desc string) error {
	// The score here will range from 1 to 4, and will be set to the
	// score associated with the highest vulnerability impact value
	// identified on the hosts in scope. For example, a single maximum
	// impact vulnerability will result in a probability score of 4.0.
	//
	// This could probably be changed to be a little more lenient.
	datacount := 0
	hostcount := 0
	highest := 1.0
	for _, x := range rs.RRA.SupportGrps {
		for _, y := range x.Host {
			hostcount++
			if !y.VulnStatus.Coverage {
				continue
			}
			datacount++
			// If we have already seen a max impact issue, just break
			if highest == 4.0 {
				break
			}
			if y.VulnStatus.Medium > 0 && highest < 2.0 {
				highest = 2.0
			}
			if y.VulnStatus.High > 0 && highest < 3.0 {
				highest = 3.0
			}
			if y.VulnStatus.Maximum > 0 && highest < 4.0 {
				highest = 4.0
			}
		}
	}
	if datacount == 0 {
		newscan := slib.RiskScenario{
			Name:     "Vulnerability scenario for " + desc,
			Coverage: "none",
			NoData:   true,
		}
		rs.Scenarios = append(rs.Scenarios, newscan)
		return nil
	}
	coverage := "complete"
	if datacount != hostcount {
		coverage = "partial"
	}
	// Set coverage to unknown as currently it is not possible to tell
	// if all hosts are being assessed; we can't go by there being no
	// known issues on the asset.
	newscen := slib.RiskScenario{
		Name:        "Vulnerability scenario for " + desc,
		Impact:      src.Impact,
		Probability: highest,
		Score:       highest * src.Impact,
		Coverage:    coverage,
		NoData:      false,
	}
	err := newscen.Validate()
	if err != nil {
		return err
	}
	rs.Scenarios = append(rs.Scenarios, newscen)

	return nil
}

// Add scenarios based on available HTTP observatory metrics
func riskHTTPObsScenario(op opContext, rs *slib.RRAServiceRisk,
	src slib.RRAAttribute, desc string) error {
	// The score here will range from 1 to 4, based on deriving the
	// value from the 0 -> 100 score returned by the httpobs scanner
	// and grader.
	totalcnt := 0
	havecnt := 0
	highest := 0.0
	for _, x := range rs.RRA.SupportGrps {
		for _, y := range x.Website {
			totalcnt++
			if !y.HTTPObs.Coverage {
				continue
			}
			havecnt++
			var uval int
			if y.HTTPObs.Score >= 75 {
				uval = 1
			} else if y.HTTPObs.Score >= 50 {
				uval = 2
			} else if y.HTTPObs.Score >= 24 {
				uval = 3
			} else {
				uval = 4
			}
			if float64(uval) >= highest {
				highest = float64(uval)
			}
		}
	}

	if havecnt == 0 {
		newscan := slib.RiskScenario{
			Name:     "httpobs scenario for " + desc,
			Coverage: "none",
			NoData:   true,
		}
		rs.Scenarios = append(rs.Scenarios, newscan)
		return nil
	}
	coverage := "complete"
	if havecnt != totalcnt {
		coverage = "partial"
	}
	// Set coverage to unknown as currently it is not possible to tell
	// if all hosts are being assessed; we can't go by there being no
	// known issues on the asset.
	newscen := slib.RiskScenario{
		Name:        "httpobs scenario for " + desc,
		Impact:      src.Impact,
		Probability: highest,
		Score:       highest * src.Impact,
		Coverage:    coverage,
		NoData:      false,
	}
	err := newscen.Validate()
	if err != nil {
		return err
	}
	rs.Scenarios = append(rs.Scenarios, newscen)

	return nil
}

// Calculate a risk scenario, uses information from the RRA
func riskRRAScenario(op opContext, rs *slib.RRAServiceRisk, src slib.RRAAttribute, desc string) error {
	newscen := slib.RiskScenario{
		Name:     "RRA derived risk for " + desc,
		Coverage: "none",
		NoData:   true,
	}
	if src.Impact != 0 && src.Probability != 0 {
		newscen.Probability = src.Probability
		newscen.Impact = src.Impact
		newscen.Score = src.Impact * src.Probability
		newscen.Coverage = "complete"
		newscen.NoData = false
	}
	err := newscen.Validate()
	if err != nil {
		return err
	}
	rs.Scenarios = append(rs.Scenarios, newscen)
	return nil
}

// Finalize calculation of the risk using available datapoints
func riskFinalize(op opContext, rs *slib.RRAServiceRisk) error {
	var (
		rvals []float64
		err   error
	)
	for _, x := range rs.Scenarios {
		// If the scenario had no data, don't include it in the
		// final scoring
		if x.NoData {
			continue
		}
		rvals = append(rvals, x.Score)
	}

	// Note the highest business impact value that was determined from
	// the RRA. This can be used as an indication of the business impact
	// for the service.
	impv := 0.0
	if rs.UsedRRAAttrib.Reputation.Impact > impv {
		impv = rs.UsedRRAAttrib.Reputation.Impact
	}
	if rs.UsedRRAAttrib.Productivity.Impact > impv {
		impv = rs.UsedRRAAttrib.Productivity.Impact
	}
	if rs.UsedRRAAttrib.Financial.Impact > impv {
		impv = rs.UsedRRAAttrib.Financial.Impact
	}
	rs.Risk.Impact = impv
	rs.Risk.ImpactLabel, err = slib.ImpactLabelFromValue(impv)
	if err != nil {
		return err
	}

	if len(rvals) == 0 {
		// This can occur if we have no metric data, including no valid
		// information in the RRA
		logf("error in risk calculation: %q has no valid scenarios", rs.RRA.Name)
		rs.Risk.Median = 0.0
		rs.Risk.Average = 0.0
		rs.Risk.WorstCase = 0.0
		rs.Risk.MedianLabel = "unknown"
		rs.Risk.AverageLabel = "unknown"
		rs.Risk.WorstCaseLabel = "unknown"
		rs.Risk.DataClass, err = slib.DataValueFromLabel(rs.RRA.DefData)
		return nil
	}
	rs.Risk.Median, err = stats.Median(rvals)
	if err != nil {
		return err
	}
	rs.Risk.MedianLabel = slib.NormalLabelFromValue(rs.Risk.Median)
	rs.Risk.Average, err = stats.Mean(rvals)
	if err != nil {
		return err
	}
	rs.Risk.AverageLabel = slib.NormalLabelFromValue(rs.Risk.Average)
	rs.Risk.WorstCase, err = stats.Max(rvals)
	if err != nil {
		return err
	}
	rs.Risk.WorstCaseLabel = slib.NormalLabelFromValue(rs.Risk.WorstCase)

	rs.Risk.DataClass, err = slib.DataValueFromLabel(rs.RRA.DefData)
	if err != nil {
		return err
	}
	return nil
}

// Determine which attributes (e.g., conf, integ, avail) from the RRA
// we want to use was impact inputs for the risk scenarios.
func riskFindHighestImpact(rs *slib.RRAServiceRisk) error {
	rs.UsedRRAAttrib.Reputation.Impact,
		rs.UsedRRAAttrib.Reputation.Probability = rs.RRA.HighestRiskReputation()
	rs.UsedRRAAttrib.Productivity.Impact,
		rs.UsedRRAAttrib.Productivity.Probability = rs.RRA.HighestRiskProductivity()
	rs.UsedRRAAttrib.Financial.Impact,
		rs.UsedRRAAttrib.Financial.Probability = rs.RRA.HighestRiskFinancial()
	return nil
}

// Risk calculation entry function, evaluates RRA in rs using any known
// datapoints we have information for
func riskCalculation(op opContext, rs *slib.RRAServiceRisk) error {
	// Determine our highest impact value
	err := riskFindHighestImpact(rs)
	if err != nil {
		return err
	}
	err = riskRRAScenario(op, rs, rs.UsedRRAAttrib.Reputation, "reputation")
	if err != nil {
		return err
	}
	err = riskRRAScenario(op, rs, rs.UsedRRAAttrib.Productivity, "productivity")
	if err != nil {
		return err
	}
	err = riskRRAScenario(op, rs, rs.UsedRRAAttrib.Financial, "financial")
	if err != nil {
		return err
	}
	err = riskComplianceScenario(op, rs, rs.UsedRRAAttrib.Reputation, "reputation")
	if err != nil {
		return err
	}
	err = riskComplianceScenario(op, rs, rs.UsedRRAAttrib.Productivity, "productivity")
	if err != nil {
		return err
	}
	err = riskComplianceScenario(op, rs, rs.UsedRRAAttrib.Financial, "financial")
	if err != nil {
		return err
	}
	err = riskVulnerabilityScenario(op, rs, rs.UsedRRAAttrib.Reputation, "reputation")
	if err != nil {
		return err
	}
	err = riskVulnerabilityScenario(op, rs, rs.UsedRRAAttrib.Productivity, "productivity")
	if err != nil {
		return err
	}
	err = riskVulnerabilityScenario(op, rs, rs.UsedRRAAttrib.Financial, "financial")
	if err != nil {
		return err
	}
	err = riskHTTPObsScenario(op, rs, rs.UsedRRAAttrib.Reputation, "reputation")
	if err != nil {
		return err
	}
	err = riskHTTPObsScenario(op, rs, rs.UsedRRAAttrib.Productivity, "productivity")
	if err != nil {
		return err
	}
	err = riskHTTPObsScenario(op, rs, rs.UsedRRAAttrib.Financial, "financial")
	if err != nil {
		return err
	}
	err = riskFinalize(op, rs)
	if err != nil {
		return err
	}
	return nil
}

// Given an RRA ID, return RRAServiceRisk representing calculated risk
// at the current time
func riskForRRA(op opContext, useCache bool, rraid int) (ret slib.RRAServiceRisk, err error) {
	// If the cache is desired, see if we have an entry in the cache for this RRA
	// and how old it is. If it is less than 4 hours old just return this.
	if useCache {
		var (
			ts  time.Time
			buf []byte
		)
		err = op.QueryRow(`SELECT timestamp, risk FROM risk WHERE
			rraid = $1 ORDER BY timestamp DESC LIMIT 1`, rraid).Scan(&ts, &buf)
		if err != nil && err != sql.ErrNoRows {
			return ret, err
		}
		// If no error was returned we got a valid hit from the cache, otherwise
		// no rows and proceed with calculation
		if err == nil {
			cutoff := time.Now().UTC().Add(-1 * (time.Minute * 60 * 4))
			if ts.After(cutoff) {
				logf("returning cached risk data for rra %v", rraid)
				err = json.Unmarshal(buf, &ret)
				if err != nil {
					return ret, err
				}
				err = ret.Validate()
				if err != nil {
					return ret, err
				}
				return ret, nil
			}
		}
	}

	r, err := getRRA(op, strconv.Itoa(rraid))
	if err != nil {
		return ret, err
	}
	// Introduce system group metadata into the RRA which datapoints may
	// use as part of processing.
	for i := range r.SupportGrps {
		err = sysGroupAddMeta(op, &r.SupportGrps[i])
		if err != nil {
			return ret, err
		}
	}

	ret.RRA = r
	err = riskCalculation(op, &ret)
	if err != nil {
		return ret, err
	}
	err = ret.Validate()
	if err != nil {
		return ret, err
	}
	return ret, nil
}

// Cache entry point, called from risk cache routine to store risk document
// for a service at current point in time
func cacheRisk(op opContext, rraid int) error {
	logf("cacherisk: processing rraid %v", rraid)
	rs, err := riskForRRA(op, false, rraid)
	if err != nil {
		return err
	}
	buf, err := json.Marshal(&rs)
	if err != nil {
		return err
	}
	// Store the generated risk document in the risks table
	_, err = op.Exec(`INSERT INTO risk
		(rraid, timestamp, risk)
		VALUES
		($1, now(), $2)`, rraid, buf)
	if err != nil {
		return err
	}
	return nil
}

func riskCacheGetRRAs(op opContext) error {
	rows, err := op.Query(`SELECT rra.rraid, MAX(risk.timestamp)
		FROM rra LEFT OUTER JOIN risk ON rra.rraid = risk.rraid
		GROUP BY rra.rraid`)
	if err != nil {
		return err
	}
	dur, err := time.ParseDuration(cfg.General.RiskCacheEvery)
	if err != nil {
		return err
	}
	cutoff := time.Now().UTC().Add(-1 * dur)
	for rows.Next() {
		var (
			rraid int
			ts    pq.NullTime
		)
		err = rows.Scan(&rraid, &ts)
		if err != nil {
			rows.Close()
			return err
		}
		if ts.Valid {
			if ts.Time.After(cutoff) {
				continue
			}
		}
		err = cacheRisk(op, rraid)
		if err != nil {
			rows.Close()
			return err
		}
	}
	err = rows.Err()
	if err != nil {
		return err
	}
	return nil
}

func riskCache() {
	defer func() {
		if e := recover(); e != nil {
			logf("error in risk cache routine: %v", e)
		}
	}()
	op := opContext{}
	op.newContext(dbconn, false, "riskcache")

	err := riskCacheGetRRAs(op)
	if err != nil {
		panic(err)
	}
}
