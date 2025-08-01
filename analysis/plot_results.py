import pandas as pd
import matplotlib.pyplot as plt
import seaborn as sns
import os
import sys

# Set a professional plot style
sns.set_theme(style="whitegrid")

# --- PATH CORRECTION ---
# Get the directory where this script is located.
SCRIPT_DIR = os.path.dirname(os.path.abspath(__file__))

# Define paths relative to the script's directory.
# This makes the script runnable from anywhere.
PROJECT_ROOT = os.path.abspath(os.path.join(SCRIPT_DIR, '..'))
RESULTS_DIR = os.path.join(PROJECT_ROOT, "results")
PLOTS_DIR = os.path.join(SCRIPT_DIR, "plots") # Create plots dir inside 'analysis'

def plot_replication_error_with_power(filename):
    """
    Plots Target vs. Actual Replicas alongside the Power Signal on a dual-axis graph.
    This provides a powerful visualization for publications.
    """
    print(f"--- Plotting Replication Error and Power Signal from {filename} ---")
    try:
        df = pd.read_csv(filename)
    except FileNotFoundError:
        print(f"Error: Data file not found at {filename}")
        return

    # --- Data Preparation ---
    optimal_power = df['OptimalPower'].iloc[0]
    # Calculate optimal replicas (the sum of actual replicas at the start or end)
    optimal_replicas = df.loc[df['CurrentPower'] == optimal_power, 'ActualReplicas'].iloc[0]

    # --- Plotting ---
    fig, ax1 = plt.subplots(figsize=(14, 8))

    # AXIS 1: REPLICAS (Left Y-axis)
    color_replicas = 'royalblue'
    ax1.set_xlabel('Experiment Step', fontsize=14)
    ax1.set_ylabel('Total Number of Replicas', color=color_replicas, fontsize=14)
    ax1.plot(df['Step'], df['TargetReplicas'], 'o--', color='black', label='Target Replicas (Ideal State)')
    ax1.plot(df['Step'], df['ActualReplicas'], '.-', color=color_replicas, markersize=10, label='Actual Replicas (Observed State)')
    ax1.tick_params(axis='y', labelcolor=color_replicas)
    ax1.axhline(optimal_replicas, color=color_replicas, linestyle=':', linewidth=2, label=f'Optimal Replicas ({int(optimal_replicas)})')
    ax1.set_ylim(bottom=0)

    # AXIS 2: POWER (Right Y-axis)
    ax2 = ax1.twinx()
    color_power = 'seagreen'
    ax2.set_ylabel('Available Power (kW)', color=color_power, fontsize=14)
    ax2.plot(df['Step'], df['CurrentPower'], '^-', color=color_power, alpha=0.7, label='Current Power (Signal)')
    ax2.tick_params(axis='y', labelcolor=color_power)
    ax2.axhline(optimal_power, color=color_power, linestyle=':', linewidth=2, label=f'Optimal Power ({int(optimal_power)} kW)')
    ax2.set_ylim(bottom=0, top=optimal_power * 1.1)

    # --- Final Touches ---
    plt.title('System Response: Replica Count vs. Available Power', fontsize=18, pad=20)
    fig.tight_layout()
    
    # Combine legends from both axes into one
    lines, labels = ax1.get_legend_handles_labels()
    lines2, labels2 = ax2.get_legend_handles_labels()
    ax2.legend(lines + lines2, labels + labels2, loc='upper center', bbox_to_anchor=(0.5, -0.1), ncol=3, fontsize=11)

    output_path = os.path.join(PLOTS_DIR, "power_vs_replicas_plot.png")
    plt.savefig(output_path, dpi=300, bbox_inches='tight') # Use bbox_inches to fit the legend
    print(f"SUCCESS: Saved plot to {output_path}")
    plt.close()

def plot_convergence_time(filename):
    """
    Plots the convergence time at each step of the ramp down.
    This visualizes the controller's reactivity.
    """
    print(f"--- Plotting Convergence Time from {filename} ---")
    try:
        df = pd.read_csv(filename)
    except FileNotFoundError:
        print(f"Error: Data file not found at {filename}")
        return
    
    avg_time = df['ConvergenceTime_ms'].mean()

    plt.figure(figsize=(12, 7))
    
    barplot = sns.barplot(x='Step', y='ConvergenceTime_ms', data=df, color='skyblue', edgecolor='black')
    
    plt.axhline(avg_time, color='r', linestyle='--', label=f'Average: {avg_time:.2f} ms')

    plt.title('Convergence Time at Each Power Reduction Step', fontsize=16)
    plt.xlabel('Ramp Down Step', fontsize=12)
    plt.ylabel('Convergence Time (ms)', fontsize=12)
    plt.legend(fontsize=11)
    plt.tight_layout()
    
    for p in barplot.patches:
        barplot.annotate(format(p.get_height(), '.0f'), 
                       (p.get_x() + p.get_width() / 2., p.get_height()), 
                       ha = 'center', va = 'center', 
                       xytext = (0, 9), 
                       textcoords = 'offset points')

    output_path = os.path.join(PLOTS_DIR, "convergence_time_plot.png")
    plt.savefig(output_path, dpi=300)
    print(f"SUCCESS: Saved plot to {output_path}")
    plt.close()

def plot_plateau_response(filename):
    """
    Plots Target vs. Actual Replicas alongside the Power Signal (as plateaus)
    on a dual-axis graph. This is the ideal visualization for this experiment.
    """
    print(f"--- Plotting Plateau Response from {filename} ---")
    try:
        df = pd.read_csv(filename)
    except FileNotFoundError:
        print(f"Error: Data file not found at {filename}")
        return

    optimal_power = df['OptimalPower'].iloc[0]
    optimal_replicas = df.loc[df['CurrentPower'] == optimal_power, 'ActualReplicas'].iloc[0]

    fig, ax1 = plt.subplots(figsize=(14, 8))

    # AXIS 1: REPLICAS (Left Y-axis)
    color_replicas = 'royalblue'
    ax1.set_xlabel('Experiment Time Step', fontsize=14)
    ax1.set_ylabel('Total Number of Replicas', color=color_replicas, fontsize=14)
    ax1.plot(df['Step'], df['ActualReplicas'], '.-', color=color_replicas, markersize=10, label='Actual Replicas (Observed State)', zorder=10)
    ax1.plot(df['Step'], df['TargetReplicas'], 'o--', color='black', alpha=0.8, label='Target Replicas (Ideal State)')
    ax1.tick_params(axis='y', labelcolor=color_replicas)
    ax1.axhline(optimal_replicas, color=color_replicas, linestyle=':', linewidth=2, label=f'Optimal Replicas ({int(optimal_replicas)})')
    ax1.set_ylim(bottom=0)
    ax1.set_xticks(df['Step']) # Ensure every step has a tick

    # AXIS 2: POWER (Right Y-axis) - Plotted as a step chart
    ax2 = ax1.twinx()
    color_power = 'seagreen'
    ax2.set_ylabel('Available Power (kW)', color=color_power, fontsize=14)
    # Use plt.step for a plateau visualization
    ax2.step(df['Step'], df['CurrentPower'], '^-', color=color_power, alpha=0.7, label='Current Power (Signal)')
    ax2.tick_params(axis='y', labelcolor=color_power)
    ax2.axhline(optimal_power, color=color_power, linestyle=':', linewidth=2, label=f'Optimal Power ({int(optimal_power)} kW)')
    ax2.set_ylim(bottom=0, top=optimal_power * 1.1)

    # Final Touches
    plt.title('System Response to Power Plateaus', fontsize=18, pad=20)
    fig.tight_layout()
    lines, labels = ax1.get_legend_handles_labels()
    lines2, labels2 = ax2.get_legend_handles_labels()
    ax2.legend(lines + lines2, labels + labels2, loc='upper center', bbox_to_anchor=(0.5, -0.1), ncol=3, fontsize=11)
    
    output_path = os.path.join(PLOTS_DIR, "plateau_response_plot.png")
    plt.savefig(output_path, dpi=300, bbox_inches='tight')
    print(f"SUCCESS: Saved plot to {output_path}")
    plt.close()

def main():
    """
    Main function to find result files and generate plots.
    """
    print("Starting plotting script...")
    print(f"Project root identified as: {PROJECT_ROOT}")
    print(f"Expecting results in: {RESULTS_DIR}")
    print(f"Will save plots to: {PLOTS_DIR}")

    if not os.path.exists(RESULTS_DIR):
        print(f"\nFATAL ERROR: Results directory '{RESULTS_DIR}' not found.")
        print("Please run 'make test-exp-ramp' and 'make test-exp-ramp-conv' first to generate the data.")
        sys.exit(1)

    if not os.path.exists(PLOTS_DIR):
        print(f"Creating plots directory: {PLOTS_DIR}")
        os.makedirs(PLOTS_DIR)

    found_files = False
    for filename in os.listdir(RESULTS_DIR):
        full_path = os.path.join(RESULTS_DIR, filename)
        if "ramp_error_data.csv" in filename:
            found_files = True
            plot_replication_error_with_power(full_path)
        elif "plateau_error_data.csv" in filename:
            plot_plateau_response(full_path)
            found_files = True
        if "full_ramp_data.csv" in filename:
            plot_replication_error_with_power(full_path)
            found_files = True
        elif "ramp_convergence_data.csv" in filename:
            found_files = True
            plot_convergence_time(full_path)

    if not found_files:
        print("\nWARNING: No result CSV files found in the results directory.")
        print("Make sure the tests ran successfully and created the files.")

if __name__ == "__main__":
    main()