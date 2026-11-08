# Image prompts

The photographs on the site were generated with the Codex CLI's
`image_generation` tool (codex-cli 0.167, Nov 2026). Each prompt was run with
two takes; the chosen take is noted. `storm-portrait.jpg` is now take 2 (see
the second round below). Output was 1672x941 (landscape) and
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

## storm-portrait.jpg (phones, take 1 of 2; replaced, see below)

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

## Second round (Nov 2026): storm-portrait take 2 and storm-passing

Run with both bookend photographs attached as references, so the ship, the
grade and the direction of travel match. `-i` takes several files, so the
prompt goes in on stdin:

```sh
codex exec --skip-git-repo-check --enable image_generation -s workspace-write -C <dir> \
  -i src/assets/img/storm-landscape.jpg -i src/assets/img/first-light.jpg < prompt.txt
```

Images land in `~/.codex/generated_images/<thread>/`. Each prompt starts:
*Use your image generation tool. Make TWO separate image generation calls
(two different takes on the brief below), each at the largest portrait
(9:16) / landscape (16:9) size your tool supports. Do nothing else. The
attached photographs are the opening shot (the storm) and the closing shot
(first light) of the same short film. Do not edit them. Generate new frames
that match their ship (dark hull, rust-red, slate-blue and grey containers,
white bridge superstructure, one amber mast lamp), their cold grade and
their film grain. The ship travels toward screen left, bow on the left side
of the ship, as in both attached frames.*

### storm-portrait.jpg take 2 (phones; take A of 2 chosen)

The first portrait take put the ship in the lower half, where a phone's
first screen shows the product window and its black floor, and sailed it to
the right, against the other two shots. The brief is storm-landscape's, as a
vertical 9:16 frame, with this composition:

> Composition for a phone screen: the camera sits low in the trough of a
> huge swell; the dark face of the next wave fills the lower two thirds of
> the frame, and the ship rides the crest beyond it, high in the frame. The
> ship, its bow lifting through the wave and its single amber mast lamp, sits
> small in the top third of the frame, slightly right of centre, with storm
> sky only in a thin strip above it. The lower two thirds are dark, heaving
> sea, spray and rain with low detail and deep blacks, leaving calm negative
> space for a title and an interface.

### storm-passing.jpg (the interlude before Also on board; take A of 2 chosen)

The middle shot: the camera pulls back across the film as the incident
calms, close in the storm, medium here, wide at first light.

> A photorealistic still frame shot on IMAX 70mm film, in the visual language
> of a Christopher Nolan film (Dunkirk, Interstellar): the same night in the
> open North Atlantic, an hour after the storm has passed. The same modern
> ocean container ship with stacked shipping containers rides a heavy, black,
> long swell, seen nearly broadside from slightly ahead. The rain has stopped
> and the wind has dropped; the sea is still huge but no longer breaking.
> Above, the lid of storm cloud is tearing open: ragged gaps show a faint,
> cold, blue-grey night light behind it. It is still deep night: no dawn, no
> sunrise glow on the horizon, no visible moon disc, no bright stars. One
> small amber mast lamp on the bridge is still lit, the only warm light in the
> frame, with a faint reflection on the wet steel. Cold, desaturated
> teal-steel and graphite grade with deep, true blacks, darker than a dawn
> scene. Wide lens, camera low near the water, enormous sense of scale, and
> quiet. Composition for a wide band that will be cropped both to a very wide
> 2.6:1 strip and to a near-square crop through its centre: the ship is
> mid-size, about a third of the frame wide, and sits inside the central half
> of the frame both horizontally and vertically, its lamp slightly above
> centre. Extremely sharp, high micro-contrast, fine natural film grain. No
> text, no logos, no lettering on containers or hull, no watermark, no people
> visible, no lens flares, nothing that looks CGI.
