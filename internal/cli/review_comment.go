package cli

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const reviewCommentMarker = "<!-- maya-stall:evidence-comment -->"

type reviewCommentAPI interface {
	Do(reviewCommentAPIRequest) (reviewCommentAPIResponse, error)
}

type reviewCommentAPIRequest struct {
	Method  string
	URL     string
	Headers map[string]string
	Body    []byte
}

type reviewCommentAPIResponse struct {
	StatusCode int
	Headers    map[string]string
	Body       []byte
}

type reviewCommentPostResult struct {
	Platform  string
	Operation string
	CommentID string
}

type reviewCommentListItem struct {
	ID     int64                       `json:"id"`
	Body   string                      `json:"body"`
	User   reviewCommentAuthorIdentity `json:"user"`
	Author reviewCommentAuthorIdentity `json:"author"`
}

type reviewCommentAuthorIdentity struct {
	Login    string `json:"login"`
	Username string `json:"username"`
}

type githubReviewCommentOptions struct {
	Repo        string
	PullRequest int
	Token       string
	BaseURL     string
}

type gitLabReviewCommentOptions struct {
	Project      string
	MergeRequest int
	Token        string
	BaseURL      string
}

type reviewCommentCommandOptions struct {
	Platform     string
	PublishedDir string
	DryRun       bool
	TokenEnv     string
	GitHub       githubReviewCommentOptions
	GitLab       gitLabReviewCommentOptions
}

func parseReviewCommentArgs(args []string, lookupEnv func(string) (string, bool)) (reviewCommentCommandOptions, error) {
	if len(args) == 0 {
		return reviewCommentCommandOptions{}, newUsageError("review-comment needs github or gitlab")
	}
	options := reviewCommentCommandOptions{Platform: args[0]}
	switch options.Platform {
	case "github":
		options.TokenEnv = "GITHUB_TOKEN"
	case "gitlab":
		options.TokenEnv = "GITLAB_TOKEN"
		options.GitLab.BaseURL = "https://gitlab.com"
	default:
		return reviewCommentCommandOptions{}, newUsageError("review-comment platform must be github or gitlab")
	}
	for i := 1; i < len(args); i++ {
		arg := args[i]
		switch arg {
		case "--dry-run":
			options.DryRun = true
		case "--token-env":
			i++
			if i >= len(args) || args[i] == "" {
				return reviewCommentCommandOptions{}, newUsageError("--token-env needs an environment variable name")
			}
			options.TokenEnv = args[i]
		case "--repo":
			if options.Platform != "github" {
				return reviewCommentCommandOptions{}, newUsageError("--repo only applies to GitHub Review Comments")
			}
			i++
			if i >= len(args) || args[i] == "" {
				return reviewCommentCommandOptions{}, newUsageError("--repo needs owner/name")
			}
			options.GitHub.Repo = args[i]
		case "--pr":
			if options.Platform != "github" {
				return reviewCommentCommandOptions{}, newUsageError("--pr only applies to GitHub Review Comments")
			}
			i++
			if i >= len(args) || args[i] == "" {
				return reviewCommentCommandOptions{}, newUsageError("--pr needs a pull request number")
			}
			number, err := strconv.Atoi(args[i])
			if err != nil || number <= 0 {
				return reviewCommentCommandOptions{}, newUsageError("--pr needs a positive pull request number")
			}
			options.GitHub.PullRequest = number
		case "--api-url":
			if options.Platform != "github" {
				return reviewCommentCommandOptions{}, newUsageError("--api-url only applies to GitHub Review Comments")
			}
			i++
			if i >= len(args) || args[i] == "" {
				return reviewCommentCommandOptions{}, newUsageError("--api-url needs a URL")
			}
			options.GitHub.BaseURL = args[i]
		case "--project":
			if options.Platform != "gitlab" {
				return reviewCommentCommandOptions{}, newUsageError("--project only applies to GitLab Review Comments")
			}
			i++
			if i >= len(args) || args[i] == "" {
				return reviewCommentCommandOptions{}, newUsageError("--project needs a GitLab project path or id")
			}
			options.GitLab.Project = args[i]
		case "--merge-request":
			if options.Platform != "gitlab" {
				return reviewCommentCommandOptions{}, newUsageError("--merge-request only applies to GitLab Review Comments")
			}
			i++
			if i >= len(args) || args[i] == "" {
				return reviewCommentCommandOptions{}, newUsageError("--merge-request needs a merge request iid")
			}
			number, err := strconv.Atoi(args[i])
			if err != nil || number <= 0 {
				return reviewCommentCommandOptions{}, newUsageError("--merge-request needs a positive merge request iid")
			}
			options.GitLab.MergeRequest = number
		case "--base-url":
			if options.Platform != "gitlab" {
				return reviewCommentCommandOptions{}, newUsageError("--base-url only applies to GitLab Review Comments")
			}
			i++
			if i >= len(args) || args[i] == "" {
				return reviewCommentCommandOptions{}, newUsageError("--base-url needs a URL")
			}
			options.GitLab.BaseURL = args[i]
		default:
			if strings.HasPrefix(arg, "-") {
				return reviewCommentCommandOptions{}, newUsageError("unknown review-comment option %q", arg)
			}
			if options.PublishedDir != "" {
				return reviewCommentCommandOptions{}, newUsageError("review-comment needs one published Evidence Bundle directory")
			}
			options.PublishedDir = arg
		}
	}
	if options.PublishedDir == "" {
		return reviewCommentCommandOptions{}, newUsageError("review-comment needs a published Evidence Bundle directory")
	}
	if options.Platform == "github" {
		if options.GitHub.Repo == "" {
			return reviewCommentCommandOptions{}, newUsageError("GitHub Review Comment needs --repo")
		}
		if options.GitHub.PullRequest <= 0 {
			return reviewCommentCommandOptions{}, newUsageError("GitHub Review Comment needs --pr")
		}
	}
	if options.Platform == "gitlab" {
		if options.GitLab.Project == "" {
			return reviewCommentCommandOptions{}, newUsageError("GitLab Review Comment needs --project")
		}
		if options.GitLab.MergeRequest <= 0 {
			return reviewCommentCommandOptions{}, newUsageError("GitLab Review Comment needs --merge-request")
		}
	}
	if !options.DryRun {
		token, ok := lookupEnv(options.TokenEnv)
		if !ok || token == "" {
			switch options.Platform {
			case "github":
				return reviewCommentCommandOptions{}, newUsageError("GitHub Review Comment needs %s", options.TokenEnv)
			case "gitlab":
				return reviewCommentCommandOptions{}, newUsageError("GitLab Review Comment needs %s", options.TokenEnv)
			}
		}
		options.GitHub.Token = token
		options.GitLab.Token = token
	}
	return options, nil
}

func postReviewComment(repoDir string, options reviewCommentCommandOptions, api reviewCommentAPI) (reviewCommentPostResult, string, error) {
	publishedDir := resolveFromRepo(repoDir, options.PublishedDir)
	manifest, err := readPublishedArtifactManifest(publishedDir)
	if err != nil {
		return reviewCommentPostResult{}, "", err
	}
	markdown := renderReviewMarkdownFromManifest(manifest)
	markdownPath := filepath.Join(publishedDir, "review-comment.md")
	if err := os.WriteFile(markdownPath, []byte(markdown), 0o644); err != nil {
		return reviewCommentPostResult{}, "", err
	}
	if options.DryRun {
		return reviewCommentPostResult{Platform: options.Platform, Operation: "dry-run"}, markdownPath, nil
	}
	if api == nil {
		api = httpReviewCommentAPI{}
	}
	switch options.Platform {
	case "github":
		result, err := postGitHubReviewComment(options.GitHub, markdown, api)
		return result, markdownPath, err
	case "gitlab":
		result, err := postGitLabReviewComment(options.GitLab, markdown, api)
		return result, markdownPath, err
	default:
		return reviewCommentPostResult{}, "", newUsageError("review-comment platform must be github or gitlab")
	}
}

func readPublishedArtifactManifest(publishedDir string) (publishedArtifactManifest, error) {
	content, err := os.ReadFile(filepath.Join(publishedDir, "artifact-manifest.json"))
	if err != nil {
		return publishedArtifactManifest{}, err
	}
	var manifest publishedArtifactManifest
	if err := json.Unmarshal(content, &manifest); err != nil {
		return publishedArtifactManifest{}, fmt.Errorf("parse published Evidence Bundle manifest: %w", err)
	}
	return manifest, nil
}

func postGitHubReviewComment(options githubReviewCommentOptions, markdown string, api reviewCommentAPI) (reviewCommentPostResult, error) {
	if err := validateGitHubReviewCommentOptions(options); err != nil {
		return reviewCommentPostResult{}, err
	}
	baseURL := strings.TrimRight(options.BaseURL, "/")
	if baseURL == "" {
		baseURL = "https://api.github.com"
	}
	headers := map[string]string{
		"Accept":        "application/vnd.github+json",
		"Authorization": "Bearer " + options.Token,
		"Content-Type":  "application/json",
	}
	marker := reviewCommentMarkerForTarget("github", fmt.Sprintf("%s#%d", options.Repo, options.PullRequest))
	markdown = reviewMarkdownForPost(markdown, marker)
	author, err := currentGitHubCommentAuthor(api, headers, baseURL)
	if err != nil {
		return reviewCommentPostResult{}, err
	}
	commentsPath := fmt.Sprintf("/repos/%s/issues/%d/comments", options.Repo, options.PullRequest)
	commentID, err := findGitHubReviewCommentID(api, headers, baseURL+commentsPath+"?per_page=100", marker, author)
	if err != nil {
		return reviewCommentPostResult{}, err
	}
	body, err := json.Marshal(map[string]string{"body": markdown})
	if err != nil {
		return reviewCommentPostResult{}, err
	}
	if commentID == "" {
		createResponse, err := api.Do(reviewCommentAPIRequest{Method: http.MethodPost, URL: baseURL + commentsPath, Headers: headers, Body: body})
		if err != nil {
			return reviewCommentPostResult{}, err
		}
		if err := requireReviewAPIStatus("create GitHub Review Comment", createResponse, http.StatusCreated); err != nil {
			return reviewCommentPostResult{}, err
		}
		createdID, err := readReviewCommentID(createResponse.Body)
		if err != nil {
			return reviewCommentPostResult{}, err
		}
		return reviewCommentPostResult{Platform: "github", Operation: "create", CommentID: createdID}, nil
	}
	updateURL := fmt.Sprintf("%s/repos/%s/issues/comments/%s", baseURL, options.Repo, url.PathEscape(commentID))
	updateResponse, err := api.Do(reviewCommentAPIRequest{Method: http.MethodPatch, URL: updateURL, Headers: headers, Body: body})
	if err != nil {
		return reviewCommentPostResult{}, err
	}
	if err := requireReviewAPIStatus("update GitHub Review Comment", updateResponse, http.StatusOK); err != nil {
		return reviewCommentPostResult{}, err
	}
	return reviewCommentPostResult{Platform: "github", Operation: "update", CommentID: commentID}, nil
}

func validateGitHubReviewCommentOptions(options githubReviewCommentOptions) error {
	if options.Repo == "" {
		return newUsageError("GitHub Review Comment needs --repo")
	}
	if !strings.Contains(options.Repo, "/") {
		return newUsageError("GitHub --repo must be owner/name")
	}
	if options.PullRequest <= 0 {
		return newUsageError("GitHub Review Comment needs --pr")
	}
	if options.Token == "" {
		return newUsageError("GitHub Review Comment needs GITHUB_TOKEN or --token-env")
	}
	return nil
}

func postGitLabReviewComment(options gitLabReviewCommentOptions, markdown string, api reviewCommentAPI) (reviewCommentPostResult, error) {
	if err := validateGitLabReviewCommentOptions(options); err != nil {
		return reviewCommentPostResult{}, err
	}
	baseURL := strings.TrimRight(options.BaseURL, "/")
	if baseURL == "" {
		baseURL = "https://gitlab.com"
	}
	headers := map[string]string{
		"Content-Type":  "application/json",
		"Private-Token": options.Token,
	}
	marker := reviewCommentMarkerForTarget("gitlab", fmt.Sprintf("merge-request:%d", options.MergeRequest))
	markdown = reviewMarkdownForPost(markdown, marker)
	author, err := currentGitLabCommentAuthor(api, headers, baseURL)
	if err != nil {
		return reviewCommentPostResult{}, err
	}
	notesPath := fmt.Sprintf("/api/v4/projects/%s/merge_requests/%d/notes", url.PathEscape(options.Project), options.MergeRequest)
	noteID, err := findGitLabReviewCommentID(api, headers, baseURL+notesPath+"?per_page=100", marker, author)
	if err != nil {
		return reviewCommentPostResult{}, err
	}
	body, err := json.Marshal(map[string]string{"body": markdown})
	if err != nil {
		return reviewCommentPostResult{}, err
	}
	if noteID == "" {
		createResponse, err := api.Do(reviewCommentAPIRequest{Method: http.MethodPost, URL: baseURL + notesPath, Headers: headers, Body: body})
		if err != nil {
			return reviewCommentPostResult{}, err
		}
		if err := requireReviewAPIStatus("create GitLab Review Comment", createResponse, http.StatusCreated); err != nil {
			return reviewCommentPostResult{}, err
		}
		createdID, err := readReviewCommentID(createResponse.Body)
		if err != nil {
			return reviewCommentPostResult{}, err
		}
		return reviewCommentPostResult{Platform: "gitlab", Operation: "create", CommentID: createdID}, nil
	}
	updateURL := fmt.Sprintf("%s%s/%s", baseURL, notesPath, url.PathEscape(noteID))
	updateResponse, err := api.Do(reviewCommentAPIRequest{Method: http.MethodPut, URL: updateURL, Headers: headers, Body: body})
	if err != nil {
		return reviewCommentPostResult{}, err
	}
	if err := requireReviewAPIStatus("update GitLab Review Comment", updateResponse, http.StatusOK); err != nil {
		return reviewCommentPostResult{}, err
	}
	return reviewCommentPostResult{Platform: "gitlab", Operation: "update", CommentID: noteID}, nil
}

func validateGitLabReviewCommentOptions(options gitLabReviewCommentOptions) error {
	if options.Project == "" {
		return newUsageError("GitLab Review Comment needs --project")
	}
	if options.MergeRequest <= 0 {
		return newUsageError("GitLab Review Comment needs --merge-request")
	}
	if options.Token == "" {
		return newUsageError("GitLab Review Comment needs GITLAB_TOKEN or --token-env")
	}
	return nil
}

func currentGitHubCommentAuthor(api reviewCommentAPI, headers map[string]string, baseURL string) (string, error) {
	response, err := api.Do(reviewCommentAPIRequest{Method: http.MethodGet, URL: baseURL + "/user", Headers: headers})
	if err != nil {
		return "", err
	}
	if response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusForbidden {
		return "github-actions[bot]", nil
	}
	if err := requireReviewAPIStatus("read GitHub authenticated user", response, http.StatusOK); err != nil {
		return "", err
	}
	var user struct {
		Login string `json:"login"`
	}
	if err := json.Unmarshal(response.Body, &user); err != nil {
		return "", fmt.Errorf("parse GitHub authenticated user: %w", err)
	}
	if user.Login == "" {
		return "", fmt.Errorf("GitHub authenticated user response missing login")
	}
	return user.Login, nil
}

func currentGitLabCommentAuthor(api reviewCommentAPI, headers map[string]string, baseURL string) (string, error) {
	response, err := api.Do(reviewCommentAPIRequest{Method: http.MethodGet, URL: baseURL + "/api/v4/user", Headers: headers})
	if err != nil {
		return "", err
	}
	if err := requireReviewAPIStatus("read GitLab authenticated user", response, http.StatusOK); err != nil {
		return "", err
	}
	var user struct {
		Username string `json:"username"`
	}
	if err := json.Unmarshal(response.Body, &user); err != nil {
		return "", fmt.Errorf("parse GitLab authenticated user: %w", err)
	}
	if user.Username == "" {
		return "", fmt.Errorf("GitLab authenticated user response missing username")
	}
	return user.Username, nil
}

func findGitHubReviewCommentID(api reviewCommentAPI, headers map[string]string, firstURL string, marker string, author string) (string, error) {
	for commentsURL := firstURL; commentsURL != ""; {
		response, err := api.Do(reviewCommentAPIRequest{Method: http.MethodGet, URL: commentsURL, Headers: headers})
		if err != nil {
			return "", err
		}
		if err := requireReviewAPIStatus("list GitHub Review Comments", response, http.StatusOK); err != nil {
			return "", err
		}
		commentID, err := findReviewCommentIDInBody(response.Body, "GitHub", marker, author)
		if err != nil || commentID != "" {
			return commentID, err
		}
		commentsURL = nextGitHubLink(response.Headers["Link"])
	}
	return "", nil
}

func findGitLabReviewCommentID(api reviewCommentAPI, headers map[string]string, firstURL string, marker string, author string) (string, error) {
	for notesURL := firstURL; notesURL != ""; {
		response, err := api.Do(reviewCommentAPIRequest{Method: http.MethodGet, URL: notesURL, Headers: headers})
		if err != nil {
			return "", err
		}
		if err := requireReviewAPIStatus("list GitLab Review Comments", response, http.StatusOK); err != nil {
			return "", err
		}
		noteID, err := findReviewCommentIDInBody(response.Body, "GitLab", marker, author)
		if err != nil || noteID != "" {
			return noteID, err
		}
		notesURL = nextGitLabPageURL(firstURL, response.Headers["X-Next-Page"])
	}
	return "", nil
}

func findReviewCommentIDInBody(body []byte, platform string, marker string, author string) (string, error) {
	var comments []reviewCommentListItem
	if err := json.Unmarshal(body, &comments); err != nil {
		return "", fmt.Errorf("parse %s Review Comments: %w", platform, err)
	}
	for _, comment := range comments {
		if strings.Contains(comment.Body, marker) && reviewCommentAuthor(comment) == author {
			return strconv.FormatInt(comment.ID, 10), nil
		}
	}
	return "", nil
}

func reviewCommentAuthor(comment reviewCommentListItem) string {
	for _, value := range []string{comment.User.Login, comment.User.Username, comment.Author.Login, comment.Author.Username} {
		if value != "" {
			return value
		}
	}
	return ""
}

func reviewCommentMarkerForTarget(platform string, target string) string {
	sum := sha256.Sum256([]byte(platform + "\x00" + target))
	return fmt.Sprintf("<!-- maya-stall:evidence-comment target-sha256:%s -->", hex.EncodeToString(sum[:]))
}

func reviewMarkdownForPost(markdown string, marker string) string {
	if strings.Contains(markdown, marker) {
		return markdown
	}
	if strings.Contains(markdown, reviewCommentMarker) {
		return strings.Replace(markdown, reviewCommentMarker, marker, 1)
	}
	return marker + "\n" + markdown
}

func nextGitHubLink(linkHeader string) string {
	for _, part := range strings.Split(linkHeader, ",") {
		sections := strings.Split(part, ";")
		if len(sections) < 2 {
			continue
		}
		if !strings.Contains(strings.Join(sections[1:], ";"), `rel="next"`) {
			continue
		}
		link := strings.TrimSpace(sections[0])
		if strings.HasPrefix(link, "<") && strings.HasSuffix(link, ">") {
			return strings.TrimSuffix(strings.TrimPrefix(link, "<"), ">")
		}
	}
	return ""
}

func nextGitLabPageURL(firstURL string, nextPage string) string {
	nextPage = strings.TrimSpace(nextPage)
	if nextPage == "" {
		return ""
	}
	parsed, err := url.Parse(firstURL)
	if err != nil {
		return ""
	}
	query := parsed.Query()
	query.Set("page", nextPage)
	parsed.RawQuery = query.Encode()
	return parsed.String()
}

func readReviewCommentID(body []byte) (string, error) {
	var response struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		return "", fmt.Errorf("parse Review Comment response: %w", err)
	}
	if response.ID == 0 {
		return "", nil
	}
	return strconv.FormatInt(response.ID, 10), nil
}

func requireReviewAPIStatus(action string, response reviewCommentAPIResponse, want int) error {
	if response.StatusCode == want {
		return nil
	}
	return fmt.Errorf("%s failed: HTTP %d: %s", action, response.StatusCode, strings.TrimSpace(string(response.Body)))
}

type httpReviewCommentAPI struct {
	Client *http.Client
}

func (api httpReviewCommentAPI) Do(request reviewCommentAPIRequest) (reviewCommentAPIResponse, error) {
	client := api.Client
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	httpRequest, err := http.NewRequest(request.Method, request.URL, bytes.NewReader(request.Body))
	if err != nil {
		return reviewCommentAPIResponse{}, err
	}
	for key, value := range request.Headers {
		httpRequest.Header.Set(key, value)
	}
	httpResponse, err := client.Do(httpRequest)
	if err != nil {
		return reviewCommentAPIResponse{}, err
	}
	defer httpResponse.Body.Close()
	body, err := io.ReadAll(httpResponse.Body)
	if err != nil {
		return reviewCommentAPIResponse{}, err
	}
	headers := make(map[string]string)
	for key, values := range httpResponse.Header {
		if len(values) > 0 {
			headers[key] = values[0]
		}
	}
	return reviewCommentAPIResponse{StatusCode: httpResponse.StatusCode, Headers: headers, Body: body}, nil
}
