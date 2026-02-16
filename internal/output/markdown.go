package output

import (
	"fmt"
	"strings"

	"github.com/TsekNet/fleet-plan/internal/diff"
)

// RenderDiffMarkdown renders diff results as markdown for MR comments.
func RenderDiffMarkdown(results []diff.DiffResult) string {
	var sb strings.Builder

	sb.WriteString("## fleet-plan\n\n")

	totalAdded, totalModified, totalDeleted := 0, 0, 0

	for _, result := range results {
		content := renderTeamMarkdown(result)
		if content == "" {
			continue
		}
		if result.Team == "(global)" {
			sb.WriteString("### Global (default.yml)\n\n")
		} else {
			sb.WriteString(fmt.Sprintf("### %s\n\n", result.Team))
		}
		sb.WriteString(content)
		sb.WriteString("\n")

		totalAdded += len(result.Policies.Added) + len(result.Queries.Added) + len(result.Software.Added) + len(result.Profiles.Added)
		totalModified += len(result.Policies.Modified) + len(result.Queries.Modified) + len(result.Software.Modified) + len(result.Profiles.Modified)
		totalDeleted += len(result.Policies.Deleted) + len(result.Queries.Deleted) + len(result.Software.Deleted) + len(result.Profiles.Deleted)
	}

	// Labels
	labelContent := renderLabelsMarkdown(results)
	if labelContent != "" {
		sb.WriteString("### Labels\n\n")
		sb.WriteString(labelContent)
		sb.WriteString("\n")
	}

	// Summary
	sb.WriteString(fmt.Sprintf("---\n**Summary:** %d added, %d modified, %d deleted\n",
		totalAdded, totalModified, totalDeleted))

	return sb.String()
}

func renderTeamMarkdown(result diff.DiffResult) string {
	var sb strings.Builder

	// Config changes (global scope)
	if len(result.Config) > 0 {
		sb.WriteString("**Config:**\n")
		for _, c := range result.Config {
			if c.Old == "" {
				sb.WriteString(fmt.Sprintf("- ➕ `%s.%s` = `%s`\n", c.Section, c.Key, c.New))
			} else {
				sb.WriteString(fmt.Sprintf("- ✏️ `%s.%s`: `%s` → `%s`\n", c.Section, c.Key, c.Old, c.New))
			}
		}
	}

	sb.WriteString(renderResourceMarkdown("Policies", result.Policies))
	sb.WriteString(renderResourceMarkdown("Queries", result.Queries))
	sb.WriteString(renderResourceMarkdown("Software", result.Software))
	sb.WriteString(renderResourceMarkdown("Profiles", result.Profiles))

	for _, e := range result.Errors {
		sb.WriteString(fmt.Sprintf("- ⚠️ %s\n", e))
	}

	return sb.String()
}

func renderResourceMarkdown(name string, rd diff.ResourceDiff) string {
	if rd.IsEmpty() {
		return ""
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("**%s:**\n", name))

	for _, c := range rd.Added {
		sb.WriteString(fmt.Sprintf("- ➕ `%s`", c.Name))
		if c.HostCount > 0 {
			sb.WriteString(fmt.Sprintf(" (~%d hosts)", c.HostCount))
		}
		sb.WriteString("\n")
	}

	for _, c := range rd.Modified {
		fieldStr := renderFieldsMarkdown(c.Fields)
		sb.WriteString(fmt.Sprintf("- ✏️ `%s` %s\n", c.Name, fieldStr))
	}

	for _, c := range rd.Deleted {
		sb.WriteString(fmt.Sprintf("- ❌ `%s`", c.Name))
		if c.Warning != "" {
			sb.WriteString(fmt.Sprintf(" ⚠️ %s", c.Warning))
		}
		sb.WriteString("\n")
	}

	return sb.String()
}

func renderFieldsMarkdown(fields map[string]diff.FieldDiff) string {
	if len(fields) == 0 {
		return ""
	}
	var parts []string
	for name, fd := range fields {
		parts = append(parts, fmt.Sprintf("`%s`: `%s` → `%s`", name, fd.Old, fd.New))
	}
	return "(" + strings.Join(parts, ", ") + ")"
}

func renderLabelsMarkdown(results []diff.DiffResult) string {
	validSeen := make(map[string]string)
	missingSeen := make(map[string]string)

	for _, result := range results {
		for _, l := range result.Labels.Valid {
			if _, ok := validSeen[l.Name]; !ok {
				validSeen[l.Name] = fmt.Sprintf("- ✅ `%s` (%d hosts)\n", l.Name, l.HostCount)
			}
		}
		for _, l := range result.Labels.Missing {
			if _, ok := missingSeen[l.Name]; !ok {
				missingSeen[l.Name] = fmt.Sprintf("- ❌ `%s` **NOT FOUND** (referenced by %s)\n", l.Name, l.ReferencedBy)
			}
		}
	}

	if len(validSeen) == 0 && len(missingSeen) == 0 {
		return ""
	}

	var sb strings.Builder
	for _, line := range validSeen {
		sb.WriteString(line)
	}
	for _, line := range missingSeen {
		sb.WriteString(line)
	}
	return sb.String()
}
