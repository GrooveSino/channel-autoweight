"""Publish and verify a source-to-assertion index for every historical CASE."""
import re
import json
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]


def manifest():
    doc = (ROOT/'docs/algorithm-casebook.md').read_text()
    cases = {int(k): v for k,v in re.findall(r'^## CASE-AW-(\d+)：(.*)$',doc,re.M)}
    refs = {2:'features.TestV501ErrorFormats',3:'firstpass.TestDuplicateAndClock',4:'firstpass.TestOutcomes',5:'firstpass.TestDecisionContract/sparse',6:'decide.TestPairSlow',7:'cmd/autoweight.TestComparison',8:'decide.TestSettle',9:'firstpass.TestDecisionContract/model_reversal',10:'weightonly.TestExecutionContract',11:'weightonly.TestExecutionContract',12:'firstpass.TestVisibleTime',13:'firstpass.TestDecisionContract/cap_wins',14:'toggle.TestDropRetired',30:'model.TestExcludeChansKeepsUnlistedTestNamedChannels'}
    text=(ROOT/'decide/casebook_test.go').read_text()
    for k,name in re.findall(r'^// CASE-AW-(\d+):[^\n]*(?:\n//[^\n]*)*\nfunc (Test\w+)',text,re.M):
        refs[int(k)]='decide.'+name
    refs.update({k:'decide.TestCasebook'+name for k,name in {
        45:'ProbeOnlyCleanWindowContinuesFloorRecoveryCappedInterval',46:'ProbeOnlyCleanWindowContinuesCappedRecoveryWhenN60ReturnsTo5',47:'ProbeOnlyCleanWindowContinuesCappedRecoveryWhenN60ReturnsTo4',48:'ProbeOnlyCleanWindowContinuesUncappedRecoveryAfterEarlyIntervalWithSingleClean10mProbe',51:'ErrorLeavingShrunkHourRecoversTrustWithCappedSampleStep',53:'MidRecoveryLeanTenMinChannelErrorDropsTrustJustBelowCapButSettleHolds',54:'ExactTwoPctLeftoverHourErrorAllowsUncappedMidRecoveryAndSkipsTrustCap',55:'CleanHourAfterExactTwoPctLeftoverContinuesUncappedMidRecovery',56:'JustOverTwoPctClientHourCutsCleanMidRecovery',58:'ShrunkClientHourAfterJustBelowPointThreeComputesUnappliedCap3',59:'ThinTenMinOneOfThreeSaturatesSevAndDropsBelowPointThreeWithoutCap'}.items()})
    assert set(refs)==set(cases)==set(range(1,60))
    return [{'id':f'CASE-AW-{k:03}', 'scenario':cases[k], 'assertion':refs[k]} for k in sorted(cases)]


class CaseIndex(unittest.TestCase):
    def test_index_and_test_symbols(self):
        rows=manifest()
        self.assertEqual(rows,json.loads((ROOT/'testdata/case-index.json').read_text()))
        for row in rows:
            pkg,case=row['assertion'].split('.')
            self.assertTrue(any('func '+case.split('/')[0]+'(' in f.read_text() for f in (ROOT/pkg).glob('*_test.go')), row)


if __name__=='__main__':
    print(json.dumps(manifest(),ensure_ascii=False,indent=2))
