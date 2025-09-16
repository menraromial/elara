import pandas as pd
import plotly.graph_objects as go
from plotly.subplots import make_subplots
import plotly.colors as pcolors
import argparse
import sys
import os
import yaml

def read_metadata_yaml(metadata_path: str) -> dict:
    """Reads the metadata YAML file into a dictionary."""
    if not os.path.exists(metadata_path):
        print(f"ℹ️  Info: Metadata file not found at '{metadata_path}'.", file=sys.stderr)
        return {}
    print(f"✅ Found metadata file at '{metadata_path}'.")
    try:
        with open(metadata_path, 'r') as f:
            return yaml.safe_load(f)
    except Exception as e:
        print(f"⚠️  Warning: Could not read or parse metadata file '{metadata_path}'. Error: {e}", file=sys.stderr)
        return {}

def add_metadata_to_legend(fig: go.Figure, metadata: dict):
    """Adds metadata to the plot's legend using invisible 'dummy' traces."""
    if not metadata:
        return

    lines = [""]
    ctrl_conf = metadata.get('controller', {})
    if ctrl_conf:
        lines.append("CONTROLLER PARAMETERS:")
        for key, value in ctrl_conf.items():
            # Convert camelCase to Title Case
            formatted_key = ''.join([' ' + char if char.isupper() else char for char in key]).strip().title()
            lines.append(f"  {formatted_key}: {value}")
    
    dep_conf = metadata.get('deployments', {})
    if dep_conf:
        lines.append("")
        lines.append("DEPLOYMENTS SETUP:")
    
    # Handle deployment groups
    for group in dep_conf.get('groups', []):
        lines.append(f"  Group: {group.get('name', 'Unknown')}")
        for member in group.get('members', []):
            weight = member.get('weight', 'N/A')
            min_replicas = member.get('minReplicas', 'N/A')
            max_replicas = member.get('maxReplicas', 'N/A')
            lines.append(f"    - {member.get('name', 'Unknown')} (W:{weight}, M:{min_replicas}, X:{max_replicas})")

    # Handle independent deployments
    for indep in dep_conf.get('independent', []):
        min_replicas = indep.get('minReplicas', 'N/A')
        max_replicas = indep.get('maxReplicas', 'N/A')
        lines.append(f"  Independent: {indep.get('name', 'Unknown')} (M:{min_replicas}, X:{max_replicas})")

    # Add invisible traces for metadata display
    for line in lines:
        fig.add_trace(go.Scatter(
            x=[None], y=[None], 
            mode='markers', 
            name=line,
            marker=dict(size=0, color='rgba(0,0,0,0)'),
            showlegend=True,
            hoverinfo='skip'
        ))

def plot_simulation_results(csv_path: str, title: str, output_dir: str = "plots"):
    """
    Reads simulation data and metadata, and generates an enhanced, professional plot
    with dynamic deployment column detection and metadata integration.
    
    Args:
        csv_path (str): Path to the CSV file containing simulation data
        title (str): Title for the plot
        output_dir (str): Directory to save the plot image
    """
    try:
        df = pd.read_csv(csv_path)
        print(f"✅ Successfully loaded data from {csv_path}")
        print(f"📊 Data shape: {df.shape}")
    except FileNotFoundError:
        print(f"❌ Error: The file '{csv_path}' was not found.", file=sys.stderr)
        sys.exit(1)
    except Exception as e:
        print(f"❌ Error reading CSV: {e}", file=sys.stderr)
        sys.exit(1)

    # --- Read Metadata ---
    base_name = os.path.splitext(os.path.basename(csv_path))[0]
    metadata_path = os.path.join(os.path.dirname(csv_path), base_name + '_metadata.yaml')
    metadata = read_metadata_yaml(metadata_path)
    
    # --- Dynamic Column Detection & Validation ---
    known_non_deployment_columns = [
        'Timestamp', 'TimeSeconds', 'PowerSignal', 'ReferencePower', 'ControllerMode', 'TotalReplicas'
    ]
    
    deployment_replica_columns = sorted([
        col for col in df.columns if col not in known_non_deployment_columns
    ])
    
    print(f"🔍 Detected Deployment Columns: {deployment_replica_columns}")
    
    # Validate required columns
    required_columns = ['TimeSeconds', 'PowerSignal', 'ReferencePower', 'TotalReplicas', 'ControllerMode']
    missing_columns = [col for col in required_columns if col not in df.columns]
    if missing_columns:
        print(f"❌ Missing required columns: {missing_columns}", file=sys.stderr)
        sys.exit(1)

    # Convert timestamps if present
    if 'Timestamp' in df.columns:
        df['Timestamp'] = pd.to_datetime(df['Timestamp'])

    # Create output directory if it doesn't exist
    os.makedirs(output_dir, exist_ok=True)

    # --- Create the enhanced plot ---
    fig = make_subplots(specs=[[{"secondary_y": True}]])

    # Enhanced color schemes
    color_cycle = pcolors.qualitative.Plotly
    
    power_colors = {
        'signal': 'royalblue',
        'reference': 'rgba(239, 85, 59, 0.7)',  # Semi-transparent red-orange
    }

    # --- Primary Y-Axis (Power) ---
    fig.add_trace(
        go.Scatter(
            x=df['TimeSeconds'], 
            y=df['PowerSignal'], 
            name='Power Signal (W)',
            mode='lines', 
            line=dict(color=power_colors['signal'], width=3),
            hovertemplate="Time: %{x:.2f}s<br>Power Signal: %{y:.2f}W<extra></extra>"
        ),
        secondary_y=False,
    )
    
    fig.add_trace(
        go.Scatter(
            x=df['TimeSeconds'], 
            y=df['ReferencePower'], 
            name='Controller Reference Power (P_ref)',
            mode='lines', 
            line=dict(color=power_colors['reference'], dash='dash', width=2),
            hovertemplate="Time: %{x:.2f}s<br>Reference Power: %{y:.2f}W<extra></extra>"
        ),
        secondary_y=False,
    )

    # --- Secondary Y-Axis (Replicas) ---
    # Plot TotalReplicas first and make it prominent
    fig.add_trace(
        go.Scatter(
            x=df['TimeSeconds'], 
            y=df['TotalReplicas'], 
            name='Total Replicas',
            mode='lines', 
            line=dict(color='black', width=4),
            hovertemplate="Time: %{x:.2f}s<br>Total Replicas: %{y}<extra></extra>"
        ),
        secondary_y=True,
    )

    # Dynamically plot each individual deployment's replica count
    for i, col_name in enumerate(deployment_replica_columns):
        color = color_cycle[i % len(color_cycle)]
        fig.add_trace(
            go.Scatter(
                x=df['TimeSeconds'], 
                y=df[col_name], 
                name=f'{col_name}',
                mode='lines', 
                line=dict(dash='dot', color=color, width=2),
                hovertemplate=f"Time: %{{x:.2f}}s<br>{col_name}: %{{y}}<extra></extra>"
            ),
            secondary_y=True,
        )

    # --- Add metadata to legend ---
    add_metadata_to_legend(fig, metadata)

    # --- Enhanced Controller Mode Annotations ---
    mode_colors = {
        "Stable": "rgba(100, 100, 100, 0.1)",
        "PendingIncrease": "rgba(255, 165, 0, 0.15)",    # Orange
        "PendingDecrease": "rgba(0, 191, 255, 0.15)",    # Deep Sky Blue
    }
    
    mode_labels = {
        "Stable": "S",
        "PendingIncrease": "I",
        "PendingDecrease": "D",
    }

    # Add mode annotations with improved logic
    if not df.empty and 'ControllerMode' in df.columns:
        last_mode = None
        mode_start_time = None
        
        for i, row in df.iterrows():
            current_mode = row['ControllerMode']
            current_time = row['TimeSeconds']
            
            if current_mode != last_mode:
                # End previous mode region
                if last_mode is not None and mode_start_time is not None:
                    fig.add_vrect(
                        x0=mode_start_time, 
                        x1=current_time,
                        fillcolor=mode_colors.get(last_mode, "rgba(220, 220, 220, 0.1)"),
                        layer="below", 
                        line_width=0,
                        annotation_text=mode_labels.get(last_mode, last_mode),
                        annotation_position="top left",
                        annotation=dict(
                            font=dict(size=14, color="black"),
                            bgcolor="rgba(255, 255, 255, 0.8)",
                            borderpad=2
                        )
                    )
                
                # Start new mode region
                last_mode = current_mode
                mode_start_time = current_time

        # Add the last region
        if last_mode is not None and mode_start_time is not None:
            final_time = df['TimeSeconds'].iloc[-1]
            if final_time > mode_start_time:
                fig.add_vrect(
                    x0=mode_start_time, 
                    x1=final_time,
                    fillcolor=mode_colors.get(last_mode, "rgba(220, 220, 220, 0.1)"),
                    layer="below", 
                    line_width=0,
                    annotation_text=mode_labels.get(last_mode, last_mode),
                    annotation_position="top left",
                    annotation=dict(
                        font=dict(size=14, color="black"),
                        bgcolor="rgba(255, 255, 255, 0.8)",
                        borderpad=2
                    )
                )

    # --- Enhanced Layout ---
    fig.update_layout(
        title=dict(
            text=f"<b>Elara Controller Scenario: {title}</b>",
            x=0.5,
            font=dict(size=20)
        ),
        xaxis=dict(
            title="<b>Time (seconds)</b>",
            showgrid=True,
            gridwidth=1,
            gridcolor="rgba(0,0,0,0.1)"
        ),
        legend=dict(
            title="<b>Metrics</b>",
            yanchor="top",
            y=1.02,
            xanchor="left",
            x=1.01,
            bgcolor='rgba(255,255,255,0.8)',
            bordercolor="Black",
            borderwidth=1,
            font=dict(family="Courier New, monospace", size=11)
        ),
        template="plotly_white",
        hovermode="x unified",
        margin=dict(l=80, r=280, t=80, b=80)  # Adjust margins to fit legend and titles
    )

    # Update y-axes with better formatting and rangemode
    fig.update_yaxes(
        title_text="<b>Power (Watts)</b>", 
        secondary_y=False,
        rangemode='tozero',
        showgrid=True,
        gridwidth=1,
        gridcolor="rgba(0,0,0,0.1)"
    )
    fig.update_yaxes(
        title_text="<b>Number of Replicas</b>", 
        secondary_y=True,
        rangemode='tozero',
        showgrid=False  # Avoid grid overlap
    )

    # --- Save Outputs ---
    safe_title = "".join(c for c in title if c.isalnum() or c in (' ', '-', '_')).rstrip().replace(' ', '_').lower()
    png_path = os.path.join(output_dir, f"{safe_title}.png")
    html_path = os.path.join(output_dir, f"{safe_title}.html")
    
    try:
        fig.write_image(png_path, width=1920, height=1080, scale=2)
        print(f"✅ PNG plot saved to: {png_path}")
    except Exception as e:
        print(f"⚠️  Warning: Could not save PNG. Is 'kaleido' installed? ('pip install kaleido'). Error: {e}", file=sys.stderr)
    
    # try:
    #     fig.write_html(html_path)
    #     print(f"✅ HTML plot saved to: {html_path}")
    # except Exception as e:
    #     print(f"⚠️  Warning: Could not save HTML. Error: {e}", file=sys.stderr)

    # --- Display Summary Statistics ---
    print(f"\n📊 --- Simulation Summary ---")
    print(f"  Time Range:      {df['TimeSeconds'].min():.2f}s to {df['TimeSeconds'].max():.2f}s")
    print(f"  Power Range:     {df['PowerSignal'].min():.2f}W to {df['PowerSignal'].max():.2f}W")
    print(f"  Replica Range:   {df['TotalReplicas'].min()} to {df['TotalReplicas'].max()}")
    print(f"  Controller Modes: {df['ControllerMode'].unique().tolist()}")
    print(f"  Deployment Columns: {len(deployment_replica_columns)}")
    if metadata:
        print(f"  Metadata Found:  ✅ Yes")
    else:
        print(f"  Metadata Found:  ❌ No")

    # Show the plot
    #fig.show()

    return fig

if __name__ == "__main__":
    parser = argparse.ArgumentParser(
        description="Plot Elara scenario results with enhanced visualization, dynamic column detection, and metadata integration.",
        formatter_class=argparse.RawDescriptionHelpFormatter,
        epilog="""
Examples:
  python3 plot_results.py test_output/csv_driven_results.csv
  python3 plot_results.py test_output/solar_simulation_results.csv --title "Solar Power Analysis" --output analysis/plots
        """
    )
    
    parser.add_argument(
        "csv_file", 
        type=str, 
        help="Path to the scenario results CSV file."
    )
    
    parser.add_argument(
        "--title",
        type=str,
        default=None,
        help="Title for the plot (default: derived from filename)"
    )
    
    parser.add_argument(
        "--output", 
        type=str, 
        default="analysis", 
        help="Output directory for plot files (default: %(default)s)"
    )

    args = parser.parse_args()

    # Use provided title or automatically derive from CSV filename
    if args.title:
        base_title = args.title
    else:
        base_title = os.path.splitext(os.path.basename(args.csv_file))[0].replace('_results', '').replace('_', ' ').title()
    
    if not os.path.exists(args.csv_file):
        print(f"❌ Error: File '{args.csv_file}' not found.", file=sys.stderr)
        sys.exit(1)

    print(f"\n🚀 Generating enhanced plot for '{base_title}'...")
    plot_simulation_results(args.csv_file, base_title, args.output)