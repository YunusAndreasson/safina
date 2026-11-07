# Image prompts

The three photographs on the site were generated with the Codex CLI's
`image_generation` tool (codex-cli 0.167, Nov 2026). Each prompt was run with
two takes; the chosen take is noted. Output was 1672x941 (landscape) and
941x1672 (portrait), saved here as high-quality JPEG in `src/assets/img/`.

```sh
codex exec --skip-git-repo-check --enable image_generation -s workspace-write -C <dir> "<prompt>"
```

Every prompt starts with: *Use your image generation tool. Make TWO separate
image generation calls (two different takes on the brief below), each at the
largest … size your tool supports.*

## storm-landscape.jpg (hero, take 1 of 2)

> A photorealistic still frame shot on IMAX 70mm film, in the visual language
> of a Christopher Nolan film (Dunkirk, Interstellar): a modern ocean container
> ship ploughing through a violent night storm in the open North Atlantic. Its
> bow lifts through a huge breaking wave; sheets of white spray and rain are
> frozen razor-sharp by a fast shutter. Stacked shipping containers on deck, wet
> steel, practical lighting only. The single warm light in the entire frame is
> one small amber mast lamp on the bridge; everything else is cold, desaturated
> teal-steel and graphite with deep, true blacks. Low camera close to the
> waterline, wide lens, enormous sense of scale and weight. Composition: the
> ship occupies the right third of the frame; the upper-left 40 percent is dark
> storm cloud and rain with low detail, leaving calm negative space for a title.
> Extremely sharp, high micro-contrast, fine natural film grain, no motion blur
> on the ship. No text, no logos, no lettering on containers or hull, no
> watermark, no people visible, no lens flares, nothing that looks CGI.

## storm-portrait.jpg (phones, take 1 of 2)

Same brief as a vertical 9:16 frame, with: *Composition for a phone screen:
the ship and wave fill the lower half; the top half is dark, low-detail storm
sky and rain, leaving calm negative space for a title.*

## first-light.jpg (closing band, take 1 of 2)

> A photorealistic still frame shot on IMAX 70mm film, in the visual language
> of a Christopher Nolan film (Dunkirk, Interstellar): the morning after a North
> Atlantic storm. A modern ocean container ship with stacked shipping containers
> holds a steady course across a heavy but calming long swell. The storm is
> breaking: a thin band of pale, cold first light on the horizon under a lid of
> dark cloud, mist on the water. One small amber mast lamp on the bridge is
> still lit, the only warm light in the frame. Cold, desaturated teal-steel and
> graphite grade with deep, true blacks. Wide lens, enormous horizon; the ship is
> small in the lower right third and the upper two thirds are sky and cloud with
> low detail, leaving calm negative space for a title. Extremely sharp, high
> micro-contrast, fine natural film grain. No text, no logos, no lettering on
> containers or hull, no watermark, no people visible, no lens flares, nothing
> that looks CGI.
