# docs/assets

`social-preview.svg` is the source for the repository's GitHub social preview
image. Render the PNG with:

```sh
rsvg-convert -w 1280 -h 640 docs/assets/social-preview.svg -o docs/assets/social-preview.png
```

If `rsvg-convert` is unavailable, fall back to
`npx --yes @resvg/resvg-js-cli docs/assets/social-preview.svg -o docs/assets/social-preview.png`,
then to ImageMagick's `magick docs/assets/social-preview.svg docs/assets/social-preview.png`.
The PNG must stay exactly 1280x640 and under 1 MB; check with
`file docs/assets/social-preview.png`.

GitHub's REST API cannot upload a repository's social preview image, so a
maintainer with admin access must upload it by hand: Settings > General >
Social preview > Edit > Upload `docs/assets/social-preview.png`.
