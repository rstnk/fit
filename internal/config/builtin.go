package config

// BuiltinPresets is written to the config path on first run. Editing the file
// afterwards is the supported way to change these.
const BuiltinPresets = `# fit presets. Constraints are declared once at the top level of a preset;
# per-kind sub-tables ([name.image], [name.video], [name.audio]) override only
# what differs for that kind. A key applies to the kind whose table it is in,
# and the audio_ keys name the audio stream of that kind: audio_bitrate under
# [name.video] is the audio inside a video, under [name.audio] it is a music
# file, and at the top level it is both.
#
# Keys: under, width, height, quality, format, fps, allow_fps_drop, bpp_floor,
#       tonemap, strip, name, audio_codec, audio_bitrate, audio_mono,
#       audio_loudnorm

[chat]
about = "under Discord's 10 MiB cap"
under = "10MiB"
width = 1280
image.width = 2048          # stills can afford more pixels than motion

[mail]
about = "under a 25 MiB mail attachment cap"
under = "25MiB"

[web]
about = "AVIF for a browser that is known to decode it"
image.format = "avif"      # avif is a still-image format, not a video one
width   = 1600
quality = 82
name    = "{stem}@{width}.{ext}"

[voice]
about = "loudness-normalised audio, video stream copied through"
audio_loudnorm = true
`
