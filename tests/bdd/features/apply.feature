Feature: axai apply
  Plan and apply the generated project, with a confirmation gate and a
  bounded fixer loop.

  Background:
    Given an AxonOps organisation "mycompany" with a cassandra cluster "prod"
    And the model returns a metric alert rule for disk usage above 80 percent

  Scenario: Apply with --yes records the outcome
    When I run axai apply "Create an alert when disk usage is over 80%" with "--yes"
    Then the exit code is 0
    And the project state is "applied"

  Scenario: Re-running an unchanged request reports no changes
    Given the request was already applied
    When I run axai apply "Create an alert when disk usage is over 80%" with "--yes"
    Then the exit code is 0
    And the output contains "no changes"

  Scenario: Declining the confirmation applies nothing
    When I run axai apply "Create an alert when disk usage is over 80%" and answer "no"
    Then the exit code is 0
    And the project state is "planned"

  Scenario: Destructive intent asks even with --yes
    Given the model returns a delete of the alert rule "High Disk"
    When I run axai apply "Delete the High Disk alert" with "--yes" and answer "no"
    Then the exit code is 0
    And nothing is destroyed

  Scenario: Apply failure stops after the fix limit
    Given the apply always fails
    When I run axai apply "Create an alert when disk usage is over 80%" with "--yes --max-fixes 2"
    Then the exit code is 3
    And the model was asked to fix exactly 2 times

  Scenario: Missing API token stops apply
    Given no AxonOps API token is set
    When I run axai apply "Create an alert when disk usage is over 80%" with "--yes"
    Then the exit code is 1
    And nothing is written to the project directory
