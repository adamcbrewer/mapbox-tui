"""Open an authenticated GPX + journey preview in the normal terminal viewer."""

import argparse
import math
from pathlib import Path
import shlex
import shutil
import subprocess
import sys
import time
import uuid

import interactions as terminal

ROOT = Path(__file__).resolve().parents[1]


def coordinates(value):
    try:
        latitude, longitude = (float(part.strip()) for part in value.split(','))
    except ValueError as error:
        raise argparse.ArgumentTypeError('use latitude,longitude') from error
    if not all(math.isfinite(v) for v in (latitude, longitude)):
        raise argparse.ArgumentTypeError('coordinates must be finite')
    if abs(latitude) > 90 or abs(longitude) > 180:
        raise argparse.ArgumentTypeError('latitude must be within ±90 and longitude within ±180')
    return f'{latitude!r}, {longitude!r}'


def keys(*values):
    terminal.tmux('send-keys', '-t', 'poc:0.0', *values)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    files = parser.add_mutually_exclusive_group()
    files.add_argument('--file', type=Path, action='append', help='loaded file; repeat for multiple imports')
    files.add_argument('--no-file', action='store_true', help='preview a journey without imported files')
    parser.add_argument('--waypoint', type=coordinates, action='append', default=[], metavar='LAT,LON')
    parser.add_argument('--mode', choices=['manual', 'walking', 'cycling', 'driving'])
    parser.add_argument('--start', type=coordinates, default='51.309174, -2.583389', metavar='LAT,LON')
    parser.add_argument('--end', type=coordinates, default='51.297454, -2.532641', metavar='LAT,LON')
    parser.add_argument('--width', type=int, default=120)
    parser.add_argument('--height', type=int, default=40)
    parser.add_argument('--capture', type=Path, help='save screenshots of all panel views, then exit')
    parser.add_argument('--detach', action='store_true', help='leave the preview running and print its attach command')
    args = parser.parse_args()
    if args.width < 70 or args.height < 18:
        parser.error('use at least 70 columns and 18 rows to seed the journey; resize freely afterwards')
    paths = args.file or ([] if args.no_file else [ROOT / 'fixtures/country-ride.gpx'])
    for path in paths:
        if not path.is_file():
            parser.error(f'file not found: {path}')
    binary = ROOT / 'bin/mapbox-tui'
    if not binary.is_file():
        parser.error('build the viewer first with make build')
    required = ['tmux'] + (['rsvg-convert'] if args.capture else [])
    for command in required:
        if shutil.which(command) is None:
            parser.error(f'{command} is required')
    if not args.capture and not args.detach and not sys.stdin.isatty():
        parser.error('interactive preview requires a terminal; use --detach or --capture DIR')

    terminal.SOCKET = 'mapbox-preview-' + uuid.uuid4().hex[:10]
    # Keep the user's credentials, configuration and provider defaults intact.
    argv = [str(binary), '--start=' + args.start, '--end=' + args.end, '--mode=' + (args.mode or 'driving')]
    argv += ['--waypoint=' + value for value in args.waypoint]
    argv += ['--file=' + str(path.resolve()) for path in paths]
    command = shlex.join(['env', 'TERM=xterm-256color', 'COLORTERM=truecolor', *argv])
    terminal.tmux('new-session', '-d', '-s', 'poc', '-x', str(args.width), '-y', str(args.height), command)
    leave_running = False
    try:
        terminal.wait_for(lambda screen: 'Context · Files · Journey' in screen and 'loading' not in screen.lower(), timeout=45)
        routing_started = time.monotonic()
        screen = terminal.wait_for(
            lambda screen: 'est ' in screen or 'Manual' in screen or 'ctrl+u error' in screen or (time.monotonic() - routing_started > 5 and 'Mapbox connection needed' in screen),
            timeout=45,
        )
        if args.mode != 'manual' and 'est ' not in screen:
            print('The plan is open, but routed estimates are unavailable. Check the viewer’s Mapbox connection/routing details.')
        if args.capture:
            args.capture.mkdir(parents=True, exist_ok=True)
            for name, shortcut in [('journey', None), ('context', 'Tab'), ('files', 'Tab')]:
                if shortcut:
                    keys(shortcut)
                title = {'journey': 'Context · Files · Journey', 'files': 'Loaded files', 'context': 'ctrl+b hide'}[name]
                terminal.wait_for(lambda screen: title in screen)
                terminal.settled()
                terminal.save_svg(args.capture / f'country-{name}.svg', args.width, args.height)
            print(f'Saved PNG and SVG previews in {args.capture.resolve()}')
        else:
            attach = ['tmux', '-L', terminal.SOCKET, 'attach-session', '-t', 'poc']
            if not args.detach:
                subprocess.run(attach, check=False)
            leave_running = subprocess.run(
                ['tmux', '-L', terminal.SOCKET, 'has-session', '-t', 'poc'],
                capture_output=True, check=False,
            ).returncode == 0
            if leave_running:
                print('Resume preview: ' + shlex.join(attach))
    except (AssertionError, subprocess.CalledProcessError) as error:
        print(f'Preview failed: {error}', file=sys.stderr)
        return 1
    finally:
        if not leave_running:
            terminal.tmux('kill-server', check=False)
    return 0


if __name__ == '__main__':
    raise SystemExit(main())
