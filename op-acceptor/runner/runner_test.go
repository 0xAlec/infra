package runner

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ethereum-optimism/infra/op-acceptor/registry"
	"github.com/ethereum-optimism/infra/op-acceptor/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func initGoModule(t *testing.T, dir string, pkgPath string) {
	t.Helper()
	cmd := exec.Command("go", "mod", "init", pkgPath)
	cmd.Dir = dir
	err := cmd.Run()
	require.NoError(t, err)
}

func setupTestRunner(t *testing.T, testContent, configContent []byte) *runner {
	// Create test directory and config file
	testDir := t.TempDir()

	// Initialize go module in test directory
	initGoModule(t, testDir, "test")

	// Create a test file in the feature directory
	featureDir := filepath.Join(testDir, "feature")
	err := os.MkdirAll(featureDir, 0755)
	require.NoError(t, err)

	// Create a test file with example tests
	err = os.WriteFile(filepath.Join(featureDir, "example_test.go"), testContent, 0644)
	require.NoError(t, err)

	// Create test validator config
	validatorConfigPath := filepath.Join(testDir, "validators.yaml")
	err = os.WriteFile(validatorConfigPath, configContent, 0644)
	require.NoError(t, err)

	// Create registry with correct paths
	reg, err := registry.NewRegistry(registry.Config{
		ValidatorConfigFile: validatorConfigPath,
	})
	require.NoError(t, err)

	r, err := NewTestRunner(Config{
		Registry: reg,
		WorkDir:  testDir,
	})
	require.NoError(t, err)
	return r.(*runner)
}

func setupDefaultTestRunner(t *testing.T) *runner {
	testContent := []byte(`
package feature_test

import "testing"

func TestOne(t *testing.T) {
	t.Log("Test one running")
}

func TestTwo(t *testing.T) {
	t.Log("Test two running")
}
`)

	configContent := []byte(`
gates:
  - id: test-gate
    description: "Test gate"
    suites:
      test-suite:
        description: "Test suite"
        tests:
          - name: TestOne
            package: "./feature"
    tests:
      - name: TestTwo
        package: "./feature"
`)
	return setupTestRunner(t, testContent, configContent)
}

func TestRunTest_SingleTest(t *testing.T) {
	r := setupDefaultTestRunner(t)

	// Create a simple test file in the work directory
	testContent := []byte(`
package main

import "testing"

func TestDirectToGate(t *testing.T) {
	t.Log("Test running")
}
`)
	err := os.WriteFile(filepath.Join(r.workDir, "main_test.go"), testContent, 0644)
	require.NoError(t, err)

	result, err := r.RunTest(types.ValidatorMetadata{
		ID:       "test1",
		Gate:     "test-gate",
		FuncName: "TestDirectToGate",
		Package:  ".",
	})

	require.NoError(t, err)
	assert.Equal(t, types.TestStatusPass, result.Status)
	assert.Nil(t, result.Error)
	assert.Equal(t, "test1", result.Metadata.ID)
	assert.Equal(t, "test-gate", result.Metadata.Gate)
	assert.Equal(t, ".", result.Metadata.Package)
	assert.False(t, result.Metadata.RunAll)
}

func TestRunTest_RunAll(t *testing.T) {
	r := setupDefaultTestRunner(t)

	result, err := r.RunTest(types.ValidatorMetadata{
		ID:      "all-tests",
		Gate:    "test-gate",
		Package: "./feature",
		RunAll:  true,
	})

	require.NoError(t, err)
	assert.Equal(t, types.TestStatusPass, result.Status)
	assert.Empty(t, result.Error)
	assert.Equal(t, "all-tests", result.Metadata.ID)
	assert.Equal(t, "test-gate", result.Metadata.Gate)
	assert.Equal(t, "./feature", result.Metadata.Package)
	assert.True(t, result.Metadata.RunAll)
}

func TestRunAllTests(t *testing.T) {
	r := setupDefaultTestRunner(t)

	// Run all tests
	result, err := r.RunAllTests()
	require.NoError(t, err)
	assert.Equal(t, types.TestStatusPass, result.Status)

	// Verify structure
	require.Contains(t, result.Gates, "test-gate", "should have test-gate")
	gate := result.Gates["test-gate"]
	assert.Equal(t, types.TestStatusPass, gate.Status)

	// Verify gate has both direct tests and suites
	assert.NotEmpty(t, gate.Tests, "should have direct tests")
	assert.NotEmpty(t, gate.Suites, "should have suites")

	// Verify suite structure
	require.Contains(t, gate.Suites, "test-suite", "should have test-suite")
	suite := gate.Suites["test-suite"]
	assert.Equal(t, types.TestStatusPass, suite.Status)
	assert.NotEmpty(t, suite.Tests, "suite should have tests")
}

func TestBuildTestArgs(t *testing.T) {
	r := setupDefaultTestRunner(t)

	tests := []struct {
		name     string
		metadata types.ValidatorMetadata
		want     []string
	}{
		{
			name: "specific test",
			metadata: types.ValidatorMetadata{
				FuncName: "TestFoo",
				Package:  "pkg/foo",
			},
			want: []string{"test", "pkg/foo", "-run", "^TestFoo$", "-count", "1", "-v"},
		},
		{
			name: "run all in package",
			metadata: types.ValidatorMetadata{
				Package: "pkg/foo",
				RunAll:  true,
			},
			want: []string{"test", "pkg/foo", "-count", "1", "-v"},
		},
		{
			name: "no package specified",
			metadata: types.ValidatorMetadata{
				FuncName: "TestFoo",
			},
			want: []string{"test", "./...", "-run", "^TestFoo$", "-count", "1", "-v"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := r.buildTestArgs(tt.metadata)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestIsValidTestName(t *testing.T) {
	tests := []struct {
		name string
		want bool
	}{
		{"TestFoo", true},
		{"", false},
		{"ok", false},
		{"?   pkg/foo", false},
		{"TestBar_SubTest", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isValidTestName(tt.name)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestFormatErrors(t *testing.T) {
	r := setupDefaultTestRunner(t)

	tests := []struct {
		name   string
		errors []string
		want   string
	}{
		{
			name:   "no errors",
			errors: nil,
			want:   "",
		},
		{
			name:   "single error",
			errors: []string{"test failed"},
			want:   "Failed tests:\ntest failed",
		},
		{
			name:   "multiple errors",
			errors: []string{"test1 failed", "test2 failed"},
			want:   "Failed tests:\ntest1 failed\ntest2 failed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := r.formatErrors(tt.errors)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestGate(t *testing.T) {
	t.Run("gate with direct tests", func(t *testing.T) {
		configContent := []byte(`
gates:
  - id: direct-test-gate
    description: "Gate with direct tests"
    tests:
      - name: TestOne
        package: "./feature"
      - name: TestTwo
        package: "./feature"
`)
		testContent := []byte(`
package feature_test

import "testing"

func TestOne(t *testing.T) {
	t.Log("Test one running")
}

func TestTwo(t *testing.T) {
	t.Log("Test two running")
}
`)
		r := setupTestRunner(t, testContent, configContent)
		result, err := r.RunAllTests()
		require.NoError(t, err)

		// Verify gate structure
		require.Contains(t, result.Gates, "direct-test-gate")
		gate := result.Gates["direct-test-gate"]
		assert.Empty(t, gate.Suites, "should have no suites")
		assert.Len(t, gate.Tests, 2, "should have two direct tests")
	})

	t.Run("gate with inheritance", func(t *testing.T) {
		configContent := []byte(`
gates:
  - id: parent-gate
    description: "Parent gate"
    tests:
      - name: TestParent
        package: "./feature"

  - id: child-gate
    description: "Child gate"
    inherits: ["parent-gate"]
    tests:
      - name: TestChild
        package: "./feature"
`)
		testContent := []byte(`
package feature_test

import "testing"

func TestParent(t *testing.T) {
	t.Log("Parent test running")
}

func TestChild(t *testing.T) {
	t.Log("Child test running")
}
`)
		r := setupTestRunner(t, testContent, configContent)
		result, err := r.RunAllTests()
		require.NoError(t, err)

		// Verify inherited tests are present
		require.Contains(t, result.Gates, "child-gate")
		childGate := result.Gates["child-gate"]
		assert.Len(t, childGate.Tests, 2, "should have both parent and child tests")
	})
}

func TestSuite(t *testing.T) {
	t.Run("suite configuration", func(t *testing.T) {
		configContent := []byte(`
gates:
  - id: suite-test-gate
    description: "Gate with suites"
    suites:
      suite-one:
        description: "First test suite"
        tests:
          - name: TestSuiteOne
            package: "./feature"
      suite-two:
        description: "Second test suite"
        tests:
          - name: TestSuiteTwo
            package: "./feature"
          - name: TestSuiteThree
            package: "./feature"
`)
		testContent := []byte(`
package feature_test

import "testing"

func TestSuiteOne(t *testing.T) {
	t.Log("Suite one test running")
}

func TestSuiteTwo(t *testing.T) {
	t.Log("Suite two test running")
}
	`)

		r := setupTestRunner(t, testContent, configContent)
		result, err := r.RunAllTests()
		require.NoError(t, err)

		// Verify suite structure
		require.Contains(t, result.Gates, "suite-test-gate")
		gate := result.Gates["suite-test-gate"]

		assert.Len(t, gate.Suites, 2, "should have two suites")

		suiteOne := gate.Suites["suite-one"]
		require.NotNil(t, suiteOne)
		assert.Len(t, suiteOne.Tests, 1, "suite-one should have one test")

		suiteTwo := gate.Suites["suite-two"]
		require.NotNil(t, suiteTwo)
		assert.Len(t, suiteTwo.Tests, 2, "suite-two should have two tests")
	})
}

// Add a new test for skipped tests
func TestRunTest_SkippedTest(t *testing.T) {
	r := setupDefaultTestRunner(t)

	// Create a test file with a skipped test
	testContent := []byte(`
package main

import "testing"

func TestSkipped(t *testing.T) {
	t.Skip("Skipping this test")
}
`)
	err := os.WriteFile(filepath.Join(r.workDir, "skip_test.go"), testContent, 0644)
	require.NoError(t, err)

	result, err := r.RunTest(types.ValidatorMetadata{
		ID:       "skip-test",
		Gate:     "test-gate",
		FuncName: "TestSkipped",
		Package:  ".",
	})

	require.NoError(t, err)
	assert.Equal(t, types.TestStatusSkip, result.Status)
	assert.Nil(t, result.Error)
}

func TestStatusDetermination(t *testing.T) {
	tests := []struct {
		name     string
		setup    func() *GateResult
		expected types.TestStatus
	}{
		{
			name: "all tests passed",
			setup: func() *GateResult {
				return &GateResult{
					Tests: map[string]*types.TestResult{
						"test1": {Status: types.TestStatusPass},
						"test2": {Status: types.TestStatusPass},
					},
				}
			},
			expected: types.TestStatusPass,
		},
		{
			name: "all tests skipped",
			setup: func() *GateResult {
				return &GateResult{
					Tests: map[string]*types.TestResult{
						"test1": {Status: types.TestStatusSkip},
						"test2": {Status: types.TestStatusSkip},
					},
				}
			},
			expected: types.TestStatusSkip,
		},
		{
			name: "mixed results with failure",
			setup: func() *GateResult {
				return &GateResult{
					Tests: map[string]*types.TestResult{
						"test1": {Status: types.TestStatusPass},
						"test2": {Status: types.TestStatusFail},
						"test3": {Status: types.TestStatusSkip},
					},
				}
			},
			expected: types.TestStatusFail,
		},
		{
			name: "mixed results without failure",
			setup: func() *GateResult {
				return &GateResult{
					Tests: map[string]*types.TestResult{
						"test1": {Status: types.TestStatusPass},
						"test2": {Status: types.TestStatusSkip},
					},
				}
			},
			expected: types.TestStatusPass,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gate := tt.setup()
			status := determineGateStatus(gate)
			assert.Equal(t, tt.expected, status)
		})
	}
}

func TestSuiteStatusDetermination(t *testing.T) {
	tests := []struct {
		name     string
		setup    func() *SuiteResult
		expected types.TestStatus
	}{
		{
			name: "empty suite",
			setup: func() *SuiteResult {
				return &SuiteResult{
					Tests: make(map[string]*types.TestResult),
				}
			},
			expected: types.TestStatusSkip,
		},
		{
			name: "all tests passed",
			setup: func() *SuiteResult {
				return &SuiteResult{
					Tests: map[string]*types.TestResult{
						"test1": {Status: types.TestStatusPass},
						"test2": {Status: types.TestStatusPass},
					},
				}
			},
			expected: types.TestStatusPass,
		},
		{
			name: "all tests skipped",
			setup: func() *SuiteResult {
				return &SuiteResult{
					Tests: map[string]*types.TestResult{
						"test1": {Status: types.TestStatusSkip},
						"test2": {Status: types.TestStatusSkip},
					},
				}
			},
			expected: types.TestStatusSkip,
		},
		{
			name: "mixed results",
			setup: func() *SuiteResult {
				return &SuiteResult{
					Tests: map[string]*types.TestResult{
						"test1": {Status: types.TestStatusPass},
						"test2": {Status: types.TestStatusSkip},
						"test3": {Status: types.TestStatusFail},
					},
				}
			},
			expected: types.TestStatusFail,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			suite := tt.setup()
			status := determineSuiteStatus(suite)
			assert.Equal(t, tt.expected, status)
		})
	}
}

func TestRunPackageTests(t *testing.T) {
	// Setup test with multiple tests in a package
	testContent := []byte(`
package feature_test

import "testing"

func TestPackageOne(t *testing.T) {
	t.Log("Test package one running")
}

func TestPackageTwo(t *testing.T) {
	t.Log("Test package two running")
}

func TestPackageThree(t *testing.T) {
	t.Log("Test package three running")
}

func TestPackageFour(t *testing.T) {
	t.Log("Test package four running")
}
`)

	configContent := []byte(`
gates:
  - id: package-gate
    description: "Package gate"
    suites:
      package-suite:
        description: "Package suite"
        tests:
          - package: "./feature"
            run_all: true
`)
	r := setupTestRunner(t, testContent, configContent)

	// Run all tests
	result, err := r.RunAllTests()
	require.NoError(t, err)
	assert.Equal(t, types.TestStatusPass, result.Status)

	// Verify structure
	require.Contains(t, result.Gates, "package-gate", "should have package-gate")
	gate := result.Gates["package-gate"]
	assert.Equal(t, types.TestStatusPass, gate.Status)

	// Verify suite structure
	require.Contains(t, gate.Suites, "package-suite", "should have package-suite")
	suite := gate.Suites["package-suite"]
	assert.Equal(t, types.TestStatusPass, suite.Status)

	// Verify tests in the suite
	assert.Len(t, suite.Tests, 1, "should have one test (the package)")

	// Get the package test
	var packageTest *types.TestResult
	for _, test := range suite.Tests {
		packageTest = test
		break
	}
	require.NotNil(t, packageTest, "package test should exist")

	// Verify the package test has subtests
	assert.NotEmpty(t, packageTest.SubTests, "package test should have subtests")
	assert.Len(t, packageTest.SubTests, 4, "should have found all 4 tests in the package")

	// Verify each subtest exists and passed
	subTestNames := []string{"TestPackageOne", "TestPackageTwo", "TestPackageThree", "TestPackageFour"}
	for _, name := range subTestNames {
		assert.Contains(t, packageTest.SubTests, name, "should have subtest "+name)
		assert.Equal(t, types.TestStatusPass, packageTest.SubTests[name].Status, name+" should be passing")
	}

	// Verify stats include all subtests
	assert.Equal(t, 5, result.Stats.Total, "stats should include all tests (1 package + 4 subtests)")
	assert.Equal(t, 5, result.Stats.Passed, "all tests should be passing")
	assert.Equal(t, 0, result.Stats.Failed, "no tests should be failing")
	assert.Equal(t, 0, result.Stats.Skipped, "no tests should be skipped")

	// Verify gate stats
	assert.Equal(t, 5, gate.Stats.Total, "gate stats should include all tests")
	assert.Equal(t, 5, gate.Stats.Passed, "all gate tests should be passing")

	// Verify suite stats
	assert.Equal(t, 5, suite.Stats.Total, "suite stats should include all tests")
	assert.Equal(t, 5, suite.Stats.Passed, "all suite tests should be passing")
}

func TestRunPackageWithFailingTests(t *testing.T) {
	// Setup test with a failing test in a package
	testContent := []byte(`
package feature_test

import "testing"

func TestFailing(t *testing.T) {
	t.Error("This test fails")
}
`)

	configContent := []byte(`
gates:
  - id: failing-gate
    description: "Gate with a failing test"
    suites:
      failing-suite:
        description: "Suite with a failing test"
        tests:
          - package: "./feature"
            run_all: true
`)
	r := setupTestRunner(t, testContent, configContent)

	// Run all tests
	result, err := r.RunAllTests()
	require.NoError(t, err)
	assert.Equal(t, types.TestStatusFail, result.Status, "overall result should be failure when any test fails")

	// Verify structure
	require.Contains(t, result.Gates, "failing-gate", "should have failing-gate")
	gate := result.Gates["failing-gate"]
	assert.Equal(t, types.TestStatusFail, gate.Status, "gate status should be failure")

	// Verify suite structure
	require.Contains(t, gate.Suites, "failing-suite", "should have failing-suite")
	suite := gate.Suites["failing-suite"]
	assert.Equal(t, types.TestStatusFail, suite.Status, "suite status should be failure")

	// Verify tests in the suite
	assert.Len(t, suite.Tests, 1, "should have one test (the package)")

	// Get the package test
	var packageTest *types.TestResult
	for _, test := range suite.Tests {
		packageTest = test
		break
	}
	require.NotNil(t, packageTest, "package test should exist")

	// Verify the package test failed
	assert.Equal(t, types.TestStatusFail, packageTest.Status, "package test should be marked as failing")
	assert.NotNil(t, packageTest.Error, "package test should have an error")

	// Verify the package test has subtests
	assert.NotEmpty(t, packageTest.SubTests, "package test should have subtests")
	assert.Len(t, packageTest.SubTests, 1, "should have found the failing test")

	// Verify the subtest has the correct status
	subTest := packageTest.SubTests["TestFailing"]
	require.NotNil(t, subTest, "should have the TestFailing subtest")
	assert.Equal(t, types.TestStatusFail, subTest.Status, "subtest should be failing")

	// Verify stats are accurate
	assert.Equal(t, 2, result.Stats.Total, "stats should include all tests (1 package + 1 subtest)")
	assert.Equal(t, 0, result.Stats.Passed, "no tests should pass")
	assert.Equal(t, 2, result.Stats.Failed, "1 subtest and parent package should fail")
	assert.Equal(t, 0, result.Stats.Skipped, "no tests should be skipped")

	// Verify gate stats
	assert.Equal(t, 2, gate.Stats.Total, "gate stats should include all tests")
	assert.Equal(t, 0, gate.Stats.Passed, "no tests should pass")
	assert.Equal(t, 2, gate.Stats.Failed, "all tests should fail")
	assert.Equal(t, 0, gate.Stats.Skipped, "no tests should be skipped")

	// Verify suite stats
	assert.Equal(t, 2, suite.Stats.Total, "suite stats should include all tests")
	assert.Equal(t, 0, suite.Stats.Passed, "no tests should pass")
	assert.Equal(t, 2, suite.Stats.Failed, "all tests should fail")
	assert.Equal(t, 0, suite.Stats.Skipped, "no tests should be skipped")
}

func TestMultiplePackageTests(t *testing.T) {
	// Setup tests in two different packages
	packageOneContent := []byte(`
package pkg1_test

import "testing"

func TestPkg1One(t *testing.T) {
	t.Log("Test pkg1 one running")
}

func TestPkg1Two(t *testing.T) {
	t.Log("Test pkg1 two running")
}
`)

	packageTwoContent := []byte(`
package pkg2_test

import "testing"

func TestPkg2One(t *testing.T) {
	t.Log("Test pkg2 one running")
}

func TestPkg2Two(t *testing.T) {
	t.Log("Test pkg2 two running")
}
`)

	configContent := []byte(`
gates:
  - id: multi-package-gate
    description: "Gate with multiple package tests"
    suites:
      multi-package-suite:
        description: "Suite with multiple package tests"
        tests:
          - package: "./pkg1"
            run_all: true
          - package: "./pkg2"
            run_all: true
`)

	// Setup the test runner with multiple packages
	r := setupTestRunner(t, nil, configContent) // Using nil for the default package

	// Create directories for each package
	pkg1Dir := filepath.Join(r.workDir, "pkg1")
	pkg2Dir := filepath.Join(r.workDir, "pkg2")

	err := os.Mkdir(pkg1Dir, 0755)
	require.NoError(t, err)
	err = os.Mkdir(pkg2Dir, 0755)
	require.NoError(t, err)

	// Write the test files
	err = os.WriteFile(filepath.Join(pkg1Dir, "pkg1_test.go"), packageOneContent, 0644)
	require.NoError(t, err)
	err = os.WriteFile(filepath.Join(pkg2Dir, "pkg2_test.go"), packageTwoContent, 0644)
	require.NoError(t, err)

	// Run all tests
	result, err := r.RunAllTests()
	require.NoError(t, err)

	// Verify structure
	require.Contains(t, result.Gates, "multi-package-gate", "should have multi-package-gate")
	gate := result.Gates["multi-package-gate"]
	assert.Equal(t, types.TestStatusPass, gate.Status, "gate status should be pass")

	// Verify suite structure
	require.Contains(t, gate.Suites, "multi-package-suite", "should have multi-package-suite")
	suite := gate.Suites["multi-package-suite"]
	assert.Equal(t, types.TestStatusPass, suite.Status, "suite status should be pass")

	// Verify tests in the suite
	assert.Len(t, suite.Tests, 2, "should have two tests (one for each package)")

	// Verify each package test has its own subtests
	var pkg1Test, pkg2Test *types.TestResult
	for _, test := range suite.Tests {
		if strings.Contains(test.Metadata.Package, "pkg1") {
			pkg1Test = test
		} else if strings.Contains(test.Metadata.Package, "pkg2") {
			pkg2Test = test
		}
	}

	require.NotNil(t, pkg1Test, "pkg1 test should exist")
	require.NotNil(t, pkg2Test, "pkg2 test should exist")

	// Verify each package test has subtests
	assert.Len(t, pkg1Test.SubTests, 2, "pkg1 should have 2 subtests")
	assert.Len(t, pkg2Test.SubTests, 2, "pkg2 should have 2 subtests")

	// Verify subtests in pkg1
	assert.Contains(t, pkg1Test.SubTests, "TestPkg1One", "should have TestPkg1One subtest")
	assert.Contains(t, pkg1Test.SubTests, "TestPkg1Two", "should have TestPkg1Two subtest")

	// Verify subtests in pkg2
	assert.Contains(t, pkg2Test.SubTests, "TestPkg2One", "should have TestPkg2One subtest")
	assert.Contains(t, pkg2Test.SubTests, "TestPkg2Two", "should have TestPkg2Two subtest")

	// Verify the stats
	assert.Equal(t, 6, result.Stats.Total, "stats should include all tests (2 packages + 4 subtests)")
	assert.Equal(t, 6, result.Stats.Passed, "all tests should be passing")
}

func TestMultiplePackageTestsInGate(t *testing.T) {
	// Setup tests in two different packages
	packageOneContent := []byte(`
package pkg1_test

import "testing"

func TestPkg1One(t *testing.T) {
	t.Log("Test pkg1 one running")
}

func TestPkg1Two(t *testing.T) {
	t.Log("Test pkg1 two running")
}
`)

	packageTwoContent := []byte(`
package pkg2_test

import "testing"

func TestPkg2One(t *testing.T) {
	t.Log("Test pkg2 one running")
}

func TestPkg2Two(t *testing.T) {
	t.Log("Test pkg2 two running")
}
`)

	configContent := []byte(`
gates:
  - id: direct-package-gate
    description: "Gate with multiple package tests as direct tests"
    tests:
      - package: "./pkg1"
        run_all: true
      - package: "./pkg2"
        run_all: true
`)

	// Setup the test runner with multiple packages
	r := setupTestRunner(t, nil, configContent) // Using nil for the default package

	// Create directories for each package
	pkg1Dir := filepath.Join(r.workDir, "pkg1")
	pkg2Dir := filepath.Join(r.workDir, "pkg2")

	err := os.Mkdir(pkg1Dir, 0755)
	require.NoError(t, err)
	err = os.Mkdir(pkg2Dir, 0755)
	require.NoError(t, err)

	// Write the test files
	err = os.WriteFile(filepath.Join(pkg1Dir, "pkg1_test.go"), packageOneContent, 0644)
	require.NoError(t, err)
	err = os.WriteFile(filepath.Join(pkg2Dir, "pkg2_test.go"), packageTwoContent, 0644)
	require.NoError(t, err)

	// Run all tests
	result, err := r.RunAllTests()
	require.NoError(t, err)

	// Verify structure
	require.Contains(t, result.Gates, "direct-package-gate", "should have direct-package-gate")
	gate := result.Gates["direct-package-gate"]
	assert.Equal(t, types.TestStatusPass, gate.Status, "gate status should be pass")

	// Verify tests in the gate
	assert.Len(t, gate.Tests, 2, "should have two tests (one for each package)")
	assert.Empty(t, gate.Suites, "should not have any suites")

	// Verify each package test has its own subtests
	var pkg1Test, pkg2Test *types.TestResult
	for _, test := range gate.Tests {
		if strings.Contains(test.Metadata.Package, "pkg1") {
			pkg1Test = test
		} else if strings.Contains(test.Metadata.Package, "pkg2") {
			pkg2Test = test
		}
	}

	require.NotNil(t, pkg1Test, "pkg1 test should exist")
	require.NotNil(t, pkg2Test, "pkg2 test should exist")

	// Verify each package test has subtests
	assert.Len(t, pkg1Test.SubTests, 2, "pkg1 should have 2 subtests")
	assert.Len(t, pkg2Test.SubTests, 2, "pkg2 should have 2 subtests")

	// Verify subtests in pkg1
	assert.Contains(t, pkg1Test.SubTests, "TestPkg1One", "should have TestPkg1One subtest")
	assert.Contains(t, pkg1Test.SubTests, "TestPkg1Two", "should have TestPkg1Two subtest")

	// Verify subtests in pkg2
	assert.Contains(t, pkg2Test.SubTests, "TestPkg2One", "should have TestPkg2One subtest")
	assert.Contains(t, pkg2Test.SubTests, "TestPkg2Two", "should have TestPkg2Two subtest")

	// Verify the stats
	assert.Equal(t, 6, result.Stats.Total, "stats should include all tests (2 packages + 4 subtests)")
	assert.Equal(t, 6, result.Stats.Passed, "all tests should be passing")
}
