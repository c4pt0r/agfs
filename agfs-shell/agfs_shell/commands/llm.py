"""
LLM command - (auto-migrated from builtins.py)
"""

from ..process import Process
from ..command_decorators import command
from . import register_command


@command(needs_path_resolution=True, supports_streaming=True)
@register_command('llm')
def cmd_llm(process: Process) -> int:
    """
    Interact with LLM models using the llm library

    Usage: llm [OPTIONS] [PROMPT]
           echo "text" | llm [OPTIONS]
           cat files | llm [OPTIONS] [PROMPT]
           cat image.jpg | llm [OPTIONS] [PROMPT]
           cat audio.wav | llm [OPTIONS] [PROMPT]

    Options:
        -m MODEL    Specify the model to use (default: gpt-4o-mini)
        -s SYSTEM   System prompt
        -k KEY      API key (overrides config/env)
        -c CONFIG   Path to config file (default: /etc/llm.yaml)

    Configuration:
        The command reads configuration from:
        1. Environment variables (e.g., OPENAI_API_KEY, ANTHROPIC_API_KEY)
        2. Config file on AGFS (default: /etc/llm.yaml)
        3. Command-line arguments (-k option)

    Config file format (YAML):
        model: gpt-4o-mini
        api_key: sk-...
        system: You are a helpful assistant

    Image Support:
        Automatically detects image input (JPEG, PNG, GIF, WebP, BMP) from stdin
        and uses vision-capable models for image analysis.

    Audio Support:
        Automatically detects audio input (WAV, MP3) from stdin, transcribes it
        using OpenAI Whisper API, then processes with the LLM.

    Examples:
        # Text prompts
        llm "What is 2+2?"
        echo "Hello world" | llm
        cat *.txt | llm "summarize these files"
        echo "Python code" | llm "translate to JavaScript"

        # Image analysis
        cat photo.jpg | llm "What's in this image?"
        cat screenshot.png | llm "Describe this screenshot in detail"
        cat diagram.png | llm

        # Audio transcription and analysis
        cat recording.wav | llm "summarize the recording"
        cat podcast.mp3 | llm "extract key points"
        cat meeting.wav | llm

        # Advanced usage
        llm -m claude-3-5-sonnet-20241022 "Explain quantum computing"
        llm -s "You are a helpful assistant" "How do I install Python?"
    """
    import sys

    try:
        import llm
    except ImportError:
        process.stderr.write(b"llm: llm library not installed. Run: pip install llm\n")
        return 1

    # Parse arguments
    model_name = None
    system_prompt = None
    api_key = None
    config_path = "/etc/llm.yaml"
    prompt_parts = []

    i = 0
    while i < len(process.args):
        arg = process.args[i]
        if arg == '-m' and i + 1 < len(process.args):
            model_name = process.args[i + 1]
            i += 2
        elif arg == '-s' and i + 1 < len(process.args):
            system_prompt = process.args[i + 1]
            i += 2
        elif arg == '-k' and i + 1 < len(process.args):
            api_key = process.args[i + 1]
            i += 2
        elif arg == '-c' and i + 1 < len(process.args):
            config_path = process.args[i + 1]
            i += 2
        else:
            prompt_parts.append(arg)
            i += 1

    # Load configuration from file if it exists
    config = {}
    try:
        if process.filesystem:
            config_content = process.filesystem.read_file(config_path)
            if config_content:
                try:
                    import yaml
                    config = yaml.safe_load(config_content.decode('utf-8'))
                    if not isinstance(config, dict):
                        config = {}
                except ImportError:
                    # If PyYAML not available, try simple key=value parsing
                    config_text = config_content.decode('utf-8')
                    config = {}
                    for line in config_text.strip().split('\n'):
                        line = line.strip()
                        if line and not line.startswith('#') and ':' in line:
                            key, value = line.split(':', 1)
                            config[key.strip()] = value.strip()
                except Exception:
                    pass  # Ignore config parse errors
    except Exception:
        pass  # Config file doesn't exist or can't be read

    # Set defaults from config or hardcoded
    if not model_name:
        model_name = config.get('model', 'gpt-4o-mini')
    if not system_prompt:
        system_prompt = config.get('system')
    if not api_key:
        api_key = config.get('api_key')

    # Set API key as environment variable (some model plugins don't support key= parameter)
    if api_key:
        import os
        if 'gpt' in model_name.lower() or 'openai' in model_name.lower():
            os.environ['OPENAI_API_KEY'] = api_key
        elif 'claude' in model_name.lower() or 'anthropic' in model_name.lower():
            os.environ['ANTHROPIC_API_KEY'] = api_key

    # Helper function to detect if binary data is an image
    def is_image(data):
        """Detect if binary data is an image by checking magic numbers"""
        if not data or len(data) < 8:
            return False
        # Check common image formats
        if data.startswith(b'\xFF\xD8\xFF'):  # JPEG
            return True
        if data.startswith(b'\x89PNG\r\n\x1a\n'):  # PNG
            return True
        if data.startswith(b'GIF87a') or data.startswith(b'GIF89a'):  # GIF
            return True
        if data.startswith(b'RIFF') and data[8:12] == b'WEBP':  # WebP
            return True
        if data.startswith(b'BM'):  # BMP
            return True
        return False

    # Helper function to detect if binary data is audio
    def is_audio(data):
        """Detect if binary data is audio by checking magic numbers"""
        if not data or len(data) < 12:
            return False
        # Check common audio formats
        if data.startswith(b'RIFF') and data[8:12] == b'WAVE':  # WAV
            return True
        if data.startswith(b'ID3') or data.startswith(b'\xFF\xFB') or data.startswith(b'\xFF\xF3') or data.startswith(b'\xFF\xF2'):  # MP3
            return True
        return False

    # Helper function to transcribe audio using OpenAI Whisper
    def transcribe_audio(audio_data, api_key=None):
        """Transcribe audio data using OpenAI Whisper API"""
        try:
            import openai
            import tempfile
            import os
        except ImportError:
            return None, "openai library not installed. Run: pip install openai"

        # Determine file extension based on audio format
        if audio_data.startswith(b'RIFF') and audio_data[8:12] == b'WAVE':
            ext = '.wav'
        else:
            ext = '.mp3'

        # Write audio data to temporary file
        with tempfile.NamedTemporaryFile(suffix=ext, delete=False) as tmp_file:
            tmp_file.write(audio_data)
            tmp_path = tmp_file.name

        try:
            # Create OpenAI client
            if api_key:
                client = openai.OpenAI(api_key=api_key)
            else:
                client = openai.OpenAI()  # Uses OPENAI_API_KEY from environment

            # Transcribe audio
            with open(tmp_path, 'rb') as audio_file:
                transcript = client.audio.transcriptions.create(
                    model="whisper-1",
                    file=audio_file
                )

            return transcript.text, None
        except Exception as e:
            return None, f"Failed to transcribe audio: {str(e)}"
        finally:
            # Clean up temporary file
            try:
                os.unlink(tmp_path)
            except Exception:
                pass

    # Get stdin content if available (keep as binary first)
    stdin_binary = None
    stdin_text = None
    # Use read() instead of get_value() to properly support streaming pipelines
    stdin_binary = process.stdin.read()
    if not stdin_binary:
        # Try to read from real stdin (but don't block if not available)
        try:
            import select
            if select.select([sys.stdin], [], [], 0.0)[0]:
                stdin_binary = sys.stdin.buffer.read()
        except Exception:
            pass  # No stdin available

    # Check if stdin is an image or audio
    is_stdin_image = False
    is_stdin_audio = False
    if stdin_binary:
        is_stdin_image = is_image(stdin_binary)
        if not is_stdin_image:
            is_stdin_audio = is_audio(stdin_binary)
            if is_stdin_audio:
                # Transcribe audio
                transcript_text, error = transcribe_audio(stdin_binary, api_key)
                if error:
                    process.stderr.write(f"llm: {error}\n".encode('utf-8'))
                    return 1
                stdin_text = transcript_text
            else:
                # Try to decode as text
                try:
                    stdin_text = stdin_binary.decode('utf-8').strip()
                except UnicodeDecodeError:
                    # Binary data but not an image or audio we recognize
                    process.stderr.write(b"llm: stdin contains binary data that is not a recognized image or audio format\n")
                    return 1

    # Get prompt from args
    prompt_text = None
    if prompt_parts:
        prompt_text = ' '.join(prompt_parts)

    # Determine the final prompt and attachments
    attachments = []
    if is_stdin_image:
        # Image input: use as attachment
        attachments.append(llm.Attachment(content=stdin_binary))
        if prompt_text:
            full_prompt = prompt_text
        else:
            full_prompt = "Describe this image"
    elif stdin_text and prompt_text:
        # Both text stdin and prompt: stdin is context, prompt is the question/instruction
        full_prompt = f"{stdin_text}\n\n===\n\n{prompt_text}"
    elif stdin_text:
        # Only text stdin: use it as the prompt
        full_prompt = stdin_text
    elif prompt_text:
        # Only prompt: use it as-is
        full_prompt = prompt_text
    else:
        # Neither: error
        process.stderr.write(b"llm: no prompt provided\n")
        return 1

    # Get the model
    try:
        model = llm.get_model(model_name)
    except Exception as e:
        error_msg = f"llm: failed to get model '{model_name}': {str(e)}\n"
        process.stderr.write(error_msg.encode('utf-8'))
        return 1

    # Prepare prompt kwargs (don't pass key - use environment variable instead)
    prompt_kwargs = {}
    if system_prompt:
        prompt_kwargs['system'] = system_prompt
    if attachments:
        prompt_kwargs['attachments'] = attachments

    # Execute the prompt
    try:
        response = model.prompt(full_prompt, **prompt_kwargs)
        output = response.text()
        process.stdout.write(output.encode('utf-8'))
        if not output.endswith('\n'):
            process.stdout.write(b'\n')
        return 0
    except Exception as e:
        error_msg = f"llm: error: {str(e)}\n"
        process.stderr.write(error_msg.encode('utf-8'))
        return 1
