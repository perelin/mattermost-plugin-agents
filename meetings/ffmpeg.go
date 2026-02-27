// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package meetings

import (
	"fmt"
	"os/exec"
)

// resolveFFMPEGPath checks for ffmpeg installation and returns the appropriate path
func resolveFFMPEGPath(pluginID string) string {
	_, standardPathErr := exec.LookPath("ffmpeg")
	if standardPathErr != nil {
		pluginPath := fmt.Sprintf("./plugins/%s/dist/ffmpeg", pluginID)
		_, pluginPathErr := exec.LookPath(pluginPath)
		if pluginPathErr != nil {
			return ""
		}
		return pluginPath
	}

	return "ffmpeg"
}
