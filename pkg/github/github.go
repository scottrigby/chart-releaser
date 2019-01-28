package github

/**
Copyright (c) 2015 tcnksm (https://github.com/tcnksm/ghr.git)
Copyright (c) 2018 The Helm Chart Maintainers

MIT License

Permission is hereby granted, free of charge, to any person obtaining
a copy of this software and associated documentation files (the
"Software"), to deal in the Software without restriction, including
without limitation the rights to use, copy, modify, merge, publish,
distribute, sublicense, and/or sell copies of the Software, and to
permit persons to whom the Software is furnished to do so, subject to
the following conditions:

The above copyright notice and this permission notice shall be
included in all copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND,
EXPRESS OR IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF
MERCHANTABILITY, FITNESS FOR A PARTICULAR PURPOSE AND
NONINFRINGEMENT. IN NO EVENT SHALL THE AUTHORS OR COPYRIGHT HOLDERS BE
LIABLE FOR ANY CLAIM, DAMAGES OR OTHER LIABILITY, WHETHER IN AN ACTION
OF CONTRACT, TORT OR OTHERWISE, ARISING FROM, OUT OF OR IN CONNECTION
WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE SOFTWARE.
**/

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/Songmu/retry"
	"github.com/pkg/errors"

	"github.com/google/go-github/github"
	"golang.org/x/oauth2"
)

// GitHub contains the functions necessary for interacting with GitHub release
// objects
type GitHub interface {
	GetRepository(ctx context.Context) (*github.Repository, error)
	CreateRelease(ctx context.Context, req *github.RepositoryRelease) (*github.RepositoryRelease, error)
	GetRelease(ctx context.Context, tag string) (*github.RepositoryRelease, error)
	EditRelease(ctx context.Context, releaseID int64, req *github.RepositoryRelease) (*github.RepositoryRelease, error)
	ListReleases(ctx context.Context) ([]*github.RepositoryRelease, error)
	UploadAsset(ctx context.Context, releaseID int64, filename string) (*github.ReleaseAsset, error)
	ListAssets(ctx context.Context, releaseID int64) ([]*github.ReleaseAsset, error)
}

// Client is the client for interacting with the GitHub API
type Client struct {
	Owner, Repo string
	*github.Client
}

// NewGitHubClient creates and initializes a new GitHubClient
func NewGitHubClient(owner, repo, token string) (GitHub, error) {
	var client *github.Client
	if token != "" {
		ts := oauth2.StaticTokenSource(&oauth2.Token{
			AccessToken: token,
		})
		tc := oauth2.NewClient(context.TODO(), ts)
		client = github.NewClient(tc)
	} else {
		client = github.NewClient(nil)
	}
	return &Client{
		Owner:  owner,
		Repo:   repo,
		Client: client,
	}, nil
}

// GetRepository fetches a repository
func (c *Client) GetRepository(ctx context.Context) (*github.Repository, error) {
	repo, res, err := c.Repositories.Get(context.TODO(), c.Owner, c.Repo)
	if err != nil {
		if res.StatusCode == http.StatusNotFound {
			return nil, nil
		}
		panic(err)
	}
	return repo, nil
}

// CreateRelease creates a new release object in the GitHub API
func (c *Client) CreateRelease(ctx context.Context, req *github.RepositoryRelease) (*github.RepositoryRelease, error) {

	release, res, err := c.Repositories.CreateRelease(context.TODO(), c.Owner, c.Repo, req)
	if err != nil {
		return nil, errors.Wrap(err, "failed to create a release")
	}

	if res.StatusCode != http.StatusCreated {
		return nil, errors.Errorf("create release: invalid status: %s", res.Status)
	}

	return release, nil
}

// GetRelease queries the GitHub API for a specified release object
func (c *Client) GetRelease(ctx context.Context, tag string) (*github.RepositoryRelease, error) {
	// Check Release whether already exists or not
	release, res, err := c.Repositories.GetReleaseByTag(context.TODO(), c.Owner, c.Repo, tag)
	if err != nil {
		if res == nil {
			return nil, errors.Wrapf(err, "failed to get release tag: %s", tag)
		}

		// TODO(tcnksm): Handle invalid token
		if res.StatusCode != http.StatusNotFound {
			return nil, errors.Wrapf(err,
				"get release tag: invalid status: %s", res.Status)
		}
		return nil, nil
	}

	return release, nil
}

// EditRelease edit a release object within the GitHub API
func (c *Client) EditRelease(ctx context.Context, releaseID int64, req *github.RepositoryRelease) (*github.RepositoryRelease, error) {
	var release *github.RepositoryRelease

	err := retry.Retry(3, 3*time.Second, func() error {
		var (
			res *github.Response
			err error
		)
		release, res, err = c.Repositories.EditRelease(context.TODO(), c.Owner, c.Repo, releaseID, req)
		if err != nil {
			return errors.Wrapf(err, "failed to edit release: %d", releaseID)
		}

		if res.StatusCode != http.StatusOK {
			return errors.Errorf("edit release: invalid status: %s", res.Status)
		}
		return nil
	})
	return release, err
}

// ListReleases lists Releases given a repository
func (c *Client) ListReleases(ctx context.Context) ([]*github.RepositoryRelease, error) {
	result := []*github.RepositoryRelease{}
	page := 1
	for {
		assets, res, err := c.Repositories.ListReleases(context.TODO(), c.Owner, c.Repo, &github.ListOptions{Page: page})
		if err != nil {
			return nil, errors.Wrap(err, "failed to list releases")
		}
		if res.StatusCode != http.StatusOK {
			return nil, errors.Errorf("list repository releases: invalid status code: %s", res.Status)
		}
		result = append(result, assets...)
		if res.NextPage <= page {
			break
		}
		page = res.NextPage
	}
	return result, nil
}

// UploadAsset uploads specified assets to a given release object
func (c *Client) UploadAsset(ctx context.Context, releaseID int64, filename string) (*github.ReleaseAsset, error) {

	filename, err := filepath.Abs(filename)
	if err != nil {
		return nil, errors.Wrap(err, "failed to get abs path")
	}

	f, err := os.Open(filename)
	if err != nil {
		return nil, errors.Wrap(err, "failed to open file")
	}

	opts := &github.UploadOptions{
		// Use base name by default
		Name: filepath.Base(filename),
	}

	var asset *github.ReleaseAsset
	err = retry.Retry(3, 3*time.Second, func() error {
		var (
			res *github.Response
			err error
		)
		asset, res, err = c.Repositories.UploadReleaseAsset(context.TODO(), c.Owner, c.Repo, releaseID, opts, f)
		if err != nil {
			return errors.Wrapf(err, "failed to upload release asset: %s", filename)
		}

		switch res.StatusCode {
		case http.StatusCreated:
			return nil
		case 422:
			return errors.Errorf(
				"upload release asset: invalid status code: %s",
				"422 (this is probably because the asset already uploaded)")
		default:
			return errors.Errorf(
				"upload release asset: invalid status code: %s", res.Status)
		}
	})
	return asset, err
}

// ListAssets lists assets associated with a given release
func (c *Client) ListAssets(ctx context.Context, releaseID int64) ([]*github.ReleaseAsset, error) {
	result := []*github.ReleaseAsset{}
	page := 1

	for {
		assets, res, err := c.Repositories.ListReleaseAssets(context.TODO(), c.Owner, c.Repo, releaseID, &github.ListOptions{Page: page})
		if err != nil {
			return nil, errors.Wrap(err, "failed to list assets")
		}

		if res.StatusCode != http.StatusOK {
			return nil, errors.Errorf("list release assets: invalid status code: %s", res.Status)
		}

		result = append(result, assets...)

		if res.NextPage <= page {
			break
		}

		page = res.NextPage
	}

	return result, nil
}
