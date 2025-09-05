import pandas as pd
import plotly.graph_objects as go
from plotly.subplots import make_subplots
import argparse
import sys
import os

def plot_simulation_results(csv_path: str, title: str, output_dir: str = "plots"):
    """
    Reads simulation data from a CSV and generates an enhanced interactive plot
    with dynamic deployment column detection.
    
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
        print("Please ensure you have run the scenario test and the CSV is in the correct directory.", file=sys.stderr)
        sys.exit(1)
    except Exception as e:
        print(f"❌ Error reading CSV: {e}", file=sys.stderr)
        sys.exit(1)

    # --- Dynamically identify deployment replica columns ---
    # These are the columns that are always present and are NOT replica counts per deployment.
    known_non_deployment_columns = [
        'Timestamp', 'TimeSeconds', 'PowerSignal', 'ReferencePower', 'ControllerMode', 'TotalReplicas'
    ]
    
    # Any other column is assumed to be a deployment replica count.
    deployment_replica_columns = [
        col for col in df.columns if col not in known_non_deployment_columns
    ]
    
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
    fig = make_subplots(
        specs=[[{"secondary_y": True}]],
        subplot_titles=[f"Elara Controller Scenario: {title}"]
    )

    # Enhanced color schemes
    power_colors = {
        'signal': '#1f77b4',      # Blue
        'reference': 'rgba(255, 0, 0, 0.6)',  # Semi-transparent red
    }
    
    replica_colors = {
        'total': '#000000',       # Black (prominent)
        'deployments': ['#ff7f0e', '#2ca02c', '#d62728', '#9467bd', '#8c564b', 
                       '#e377c2', '#7f7f7f', '#bcbd22', '#17becf', '#aec7e8']  # Plotly default cycle
    }

    # --- Primary Y-Axis (Power) ---
    fig.add_trace(
        go.Scatter(
            x=df['TimeSeconds'], 
            y=df['PowerSignal'], 
            name='Power Signal (W)',
            mode='lines', 
            line=dict(color=power_colors['signal'], width=3),
            hovertemplate="<b>Power Signal</b><br>Time: %{x}s<br>Power: %{y}W<extra></extra>"
        ),
        secondary_y=False,
    )
    
    fig.add_trace(
        go.Scatter(
            x=df['TimeSeconds'], 
            y=df['ReferencePower'], 
            name='Reference Power (P_ref)',
            mode='lines', 
            line=dict(color=power_colors['reference'], dash='dash', width=2),
            hovertemplate="<b>Reference Power</b><br>Time: %{x}s<br>Power: %{y}W<extra></extra>"
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
            line=dict(color=replica_colors['total'], width=4),
            hovertemplate="<b>Total Replicas</b><br>Time: %{x}s<br>Replicas: %{y}<extra></extra>"
        ),
        secondary_y=True,
    )

    # Dynamically plot each individual deployment's replica count
    for i, col_name in enumerate(deployment_replica_columns):
        color_idx = i % len(replica_colors['deployments'])
        fig.add_trace(
            go.Scatter(
                x=df['TimeSeconds'], 
                y=df[col_name], 
                name=f'{col_name} Replicas',
                mode='lines', 
                line=dict(
                    color=replica_colors['deployments'][color_idx], 
                    dash='dot', 
                    width=2
                ),
                hovertemplate=f"<b>{col_name} Replicas</b><br>Time: %{{x}}s<br>Replicas: %{{y}}<extra></extra>"
            ),
            secondary_y=True,
        )

    # --- Enhanced Controller Mode Annotations ---
    mode_colors = {
        "Stable": "rgba(100, 100, 100, 0.15)",
        "PendingIncrease": "rgba(255, 165, 0, 0.25)",    # Orange
        "PendingDecrease": "rgba(0, 191, 255, 0.25)",    # Deep Sky Blue
    }
    
    mode_labels = {
        "Stable": "S",
        "PendingIncrease": "I",
        "PendingDecrease": "D",
    }

    # Add mode annotations with improved logic
    last_mode = None
    mode_start_time = None
    
    for i, row in df.iterrows():
        current_mode = row['ControllerMode']
        current_time = row['TimeSeconds']
        
        if current_mode != last_mode:
            # End previous mode region
            if last_mode is not None and mode_start_time is not None and i > 0:
                fig.add_vrect(
                    x0=mode_start_time, 
                    x1=current_time,
                    fillcolor=mode_colors.get(last_mode, "rgba(220, 220, 220, 0.15)"),
                    layer="below", 
                    line_width=0,
                    annotation_text=mode_labels.get(last_mode, last_mode),
                    annotation_position="top left",
                    annotation=dict(
                        font=dict(size=12, color="black"),
                        bgcolor="rgba(255, 255, 255, 0.8)",
                        bordercolor="black",
                        borderwidth=1
                    )
                )
            
            # Start new mode region
            last_mode = current_mode
            mode_start_time = current_time

    # Add the last region with improved logic
    if last_mode is not None and mode_start_time is not None:
        final_time = df['TimeSeconds'].iloc[-1]
        if final_time > mode_start_time:  # Only add if there's a meaningful duration
            fig.add_vrect(
                x0=mode_start_time, 
                x1=final_time,
                fillcolor=mode_colors.get(last_mode, "rgba(220, 220, 220, 0.15)"),
                layer="below", 
                line_width=0,
                annotation_text=mode_labels.get(last_mode, last_mode),
                annotation_position="top left",
                annotation=dict(
                    font=dict(size=12, color="black"),
                    bgcolor="rgba(255, 255, 255, 0.8)",
                    bordercolor="black",
                    borderwidth=1
                )
            )

    # --- Enhanced Layout ---
    fig.update_layout(
        title=dict(
            text=f"<b>Elara Controller Scenario: {title}</b>",
            x=0.5,
            font=dict(size=18)
        ),
        xaxis_title="<b>Time (seconds)</b>",
        legend=dict(
            title="<b>Metrics</b>",
            orientation="v",
            yanchor="top",
            y=1,
            xanchor="left",
            x=1.02
        ),
        template="plotly_white",
        hovermode="x unified",  # Improved tooltip experience
        width=1200,
        height=600,
        margin=dict(r=150)  # Right margin for legend
    )

    # Update y-axes with better formatting and rangemode
    fig.update_yaxes(
        title_text="<b>Power (Watts)</b>", 
        secondary_y=False,
        rangemode='tozero',
        showgrid=True,
        gridwidth=1,
        gridcolor="rgba(128, 128, 128, 0.2)"
    )
    fig.update_yaxes(
        title_text="<b>Number of Replicas</b>", 
        secondary_y=True,
        rangemode='tozero',
        showgrid=True,
        gridwidth=1,
        gridcolor="rgba(128, 128, 128, 0.2)"
    )

    # Update x-axis
    fig.update_xaxes(
        showgrid=True,
        gridwidth=1,
        gridcolor="rgba(128, 128, 128, 0.2)"
    )

    # Save the plot with better filename handling
    safe_title = "".join(c for c in title if c.isalnum() or c in (' ', '-', '_')).rstrip()
    filename = f"{safe_title.replace(' ', '_').lower()}_plot"
    
    # Save as both PNG and HTML
    png_path = os.path.join(output_dir, f"{filename}.png")
    html_path = os.path.join(output_dir, f"{filename}.html")
    
    try:
        fig.write_image(png_path, width=1200, height=600, scale=2)
        print(f"✅ PNG plot saved to: {png_path}")
    except Exception as e:
        print(f"⚠️  Warning: Could not save PNG (install kaleido with 'pip install kaleido'): {e}")
    
    try:
        fig.write_html(html_path)
        print(f"✅ HTML plot saved to: {html_path}")
    except Exception as e:
        print(f"⚠️  Warning: Could not save HTML: {e}")

    # Display plot statistics
    print(f"\n📊 Plot Statistics:")
    print(f"   - Time range: {df['TimeSeconds'].min():.1f}s to {df['TimeSeconds'].max():.1f}s")
    print(f"   - Power range: {df['PowerSignal'].min():.1f}W to {df['PowerSignal'].max():.1f}W")
    print(f"   - Replica range: {df['TotalReplicas'].min()} to {df['TotalReplicas'].max()}")
    print(f"   - Controller modes: {df['ControllerMode'].unique().tolist()}")
    print(f"   - Deployment columns detected: {len(deployment_replica_columns)}")

    # Show the plot
    fig.show()

    return fig

if __name__ == "__main__":
    parser = argparse.ArgumentParser(
        description="Plot Elara scenario results with enhanced visualization and dynamic column detection.",
        formatter_class=argparse.RawDescriptionHelpFormatter,
        epilog="""
Examples:
  python plot_results.py test_output/my_results.csv
  python plot_results.py test_output/scenario.csv --title "My Custom Scenario" --output plots/
        """
    )
    
    parser.add_argument(
        "csv_file",
        type=str,
        help="Path to the scenario CSV file (e.g., test_output/my_results.csv)"
    )
    
    parser.add_argument(
        "--title",
        type=str,
        default="Scenario Analysis",
        help="Title for the plot (default: %(default)s)"
    )
    
    parser.add_argument(
        "--output",
        type=str,
        default="plots",
        help="Output directory for plot files (default: %(default)s)"
    )

    args = parser.parse_args()

    if not os.path.exists(args.csv_file):
        print(f"❌ Error: File '{args.csv_file}' not found.", file=sys.stderr)
        sys.exit(1)

    print(f"🚀 Generating enhanced plot for: {args.csv_file}")
    plot_simulation_results(args.csv_file, args.title, args.output)