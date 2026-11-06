Feature: Requests add to an existing project
  Running axai again on the same project adds to it. Existing resources are
  never replaced or removed unless the request asks to change or remove them.

  Background:
    Given an AxonOps organisation "mycompany" with a cassandra cluster "prod"
    And the project already has a free-space counter chart

  Scenario: A second request adds a resource and keeps the first
    Given the model returns a metric alert rule for disk usage above 80 percent
    When I run axai plan "Create critical alert when disk over 80%" with backend "terraform"
    Then the exit code is 0
    And the project contains "main.tf" with resource "axonops_metric_alert_rule"
    And the project contains "main.tf" with resource "axonops_custom_dashboard"

  Scenario: Asking for the same thing again changes nothing
    Given the model returns the same free-space counter chart again
    When I run axai plan "Create a counter chart that shows the cluster free space" with backend "terraform"
    Then the exit code is 0
    And the output contains "unchanged custom_dashboard/Free Space"

  Scenario: A create that would override an existing resource is refused
    Given the model returns a different free-space counter chart with the same name
    When I run axai plan "Create a counter chart showing memory" with backend "terraform"
    Then the exit code is 2
    And the output contains nothing about success
    And the project contains "main.tf" with resource "100 - avg(host_Disk_UsedPercent"

  Scenario: An explicit update changes only the fields given
    Given the model returns a metric alert rule for disk usage above 80 percent
    And I plan that alert
    And the model updates the alert duration to "30m"
    When I run axai plan "Make the disk alert last 30 minutes" with backend "terraform"
    Then the exit code is 0
    And the project contains "main.tf" with resource "30m"
    And the project contains "main.tf" with resource "axonops_custom_dashboard"
    And the project contains "main.tf" with resource "critical_value = 80"

  Scenario: A different cluster cannot be mixed into the project
    Given the model returns a metric alert rule for disk usage above 80 percent
    When I run axai plan against cluster "other" with backend "terraform"
    Then the exit code is 2
