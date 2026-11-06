Feature: axai plan
  Generate a reusable Terraform or Ansible project from a natural-language
  request and show what it would change, without applying anything.

  Background:
    Given an AxonOps organisation "mycompany" with a cassandra cluster "prod"
    And the model returns a metric alert rule for disk usage above 80 percent

  Scenario: Terraform project is generated for a metric alert request
    When I run axai plan "Create an alert when disk usage is over 80%" with backend "terraform"
    Then the exit code is 0
    And the project contains "main.tf" with resource "axonops_metric_alert_rule"
    And the project contains "versions.tf" pinning provider "axonops/axonops"
    And no generated file contains the API token

  Scenario: Ansible project is generated for the same request
    When I run axai plan "Create an alert when disk usage is over 80%" with backend "ansible"
    Then the exit code is 0
    And the project contains "group_vars/all/axonops.yml" with variable "axonops_alert_rules"
    And the project contains "site.yml" using role "axonops.axonops.configurations"

  Scenario: Empty request is rejected
    When I run axai plan "" with backend "terraform"
    Then the exit code is 1

  Scenario: Resource kind outside the catalogue is rejected
    Given the model returns a resource of kind "aws_s3_bucket"
    When I run axai plan "Create a bucket" with backend "terraform"
    Then the exit code is 2
    And nothing is written to the project directory

  Scenario: Inline credential is rejected
    Given the model returns a Slack integration with an inline webhook URL
    When I run axai plan "Send alerts to Slack" with backend "terraform"
    Then the exit code is 2

  Scenario: Counter chart for cluster free space
    Given the model returns a counter chart for cluster free space
    When I run axai plan "Create a counter chart that shows the cluster free space" with backend "terraform"
    Then the exit code is 0
    And the project contains "main.tf" with resource "axonops_custom_dashboard"
    And the project contains "main.tf" with resource "metrics_counter"

  Scenario: Counter chart is not generated for Ansible yet
    Given the model returns a counter chart for cluster free space
    When I run axai plan "Create a counter chart that shows the cluster free space" with backend "ansible"
    Then the exit code is 2

  Scenario: Alert routed to Slack keeps the webhook URL out of the project and the model
    Given the model returns a free-space dashboard, an alert, a Slack integration and a route
    When I run axai plan "Chart free space, alert over 80 percent to Slack using https://hooks.slack.com/services/T0/B0/SECRETVALUE" with backend "terraform"
    Then the exit code is 0
    And the project contains "main.tf" with resource "axonops_slack_integration"
    And the project contains "main.tf" with resource "integration_name = axonops_slack_integration.ops_slack.name"
    And no generated file contains "SECRETVALUE"
    And the model never saw "SECRETVALUE"

  Scenario: A part of the request the catalogue cannot express stops the run
    Given the model reports that "send an SMS" is unsupported
    When I run axai plan "Create an alert and send an SMS" with backend "terraform"
    Then the exit code is 2
    And nothing is written to the project directory

  Scenario: Missing API token plans offline with a warning
    Given no AxonOps API token is set
    When I run axai plan "Create an alert when disk usage is over 80%" with backend "terraform"
    Then the exit code is 0
    And the warning contains "AXONOPS_API_TOKEN is not set"
    And the project contains "main.tf" with resource "axonops_metric_alert_rule"
    And the backend plan was not run
